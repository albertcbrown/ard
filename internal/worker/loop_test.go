package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

func TestLoopBasics(t *testing.T) {
	l := New(Deps{})
	if l.Name() != "workers" || !l.Claims() {
		t.Error("the worker loop claims, so it is skipped while paused")
	}
	if l.Interval(nil) != 5*time.Minute || l.TickTimeout(nil) != 5*time.Minute {
		t.Error("defaults")
	}
	cfg := &config.Resolved{}
	cfg.Intervals.Workers = config.Duration(7 * time.Minute)
	if l.Interval(cfg) != 7*time.Minute {
		t.Error("interval from config")
	}
}

func (h *harness) only() finishCall {
	h.t.Helper()
	got := h.fin.got()
	if len(got) != 1 {
		h.t.Fatalf("FinishWith calls = %d, want 1", len(got))
	}
	return got[0]
}

func noTests(h *harness) { h.cfg.Roles[0].Tests = "" }

func TestSummaryHostPathsReplaced(t *testing.T) {
	h := newHarness(t, noTests)
	home := t.TempDir()
	h.loop.d.HostPaths = closeout.HostPaths{Home: home, ArdRoot: filepath.Join(home, ".ard")}
	h.issue("Crash on start", "type:bug")
	var wt string
	run := func(role string) {
		h.agentWrites(map[string]string{"hello.txt": "bye"}, role, nil)
		inner := h.ex.run
		h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int { wt = hd.Worktree; return inner(kind, j, hd) }
	}
	run("---\nrole: engineer\nstatus: done\nattempt: 1\n---\nWorked in @WT@/sub and " + home + "/.ard/logs.\n")
	h.mustTick()
	if h.code.n() != 1 || wt == "" {
		t.Fatalf("code commits = %d, worktree %q", h.code.n(), wt)
	}
	prs := h.prs()
	if len(prs) != 1 {
		t.Fatalf("prs = %d", len(prs))
	}
	for name, body := range map[string]string{"commit body": h.code.reqs[0].Body, "PR body": prs[0].Body} {
		if strings.Contains(body, wt) || strings.Contains(body, home) {
			t.Errorf("%s holds a host path: %q", name, body)
		}
		if !strings.Contains(body, "/work/sub") || !strings.Contains(body, "~/.ard/logs") {
			t.Errorf("%s lacks the placeholders: %q", name, body)
		}
	}

}

func TestHappyPathNoTests(t *testing.T) {
	h := newHarness(t, noTests)
	n := h.issue("Crash on start", "type:bug")
	h.seedDeferred(n, "license-review", "ghost-review")
	h.agentWrites(map[string]string{"hello.txt": "bye", "sub/run.sh": "x"}, roleDone, nil)
	h.mustTick()

	if h.code.n() != 1 {
		t.Fatalf("code commits = %d", h.code.n())
	}
	req, branch := h.code.reqs[0], h.code.br[0]
	wantBranch := fmt.Sprintf("worker/engineer-%d-crash-on-start", n)
	if branch != wantBranch || !req.Create || req.Type != "fix" || req.Role != "engineer" || req.Base != h.cfg.SHA || req.Issue != n || req.Title != "Crash on start" {
		t.Errorf("branch %q, req %+v", branch, req)
	}
	if h.code.files[0]["hello.txt"] != "bye" || len(req.Files) != 2 {
		t.Errorf("files = %v", h.code.files[0])
	}
	if !strings.Contains(req.Body, "Fixes #99") { // CodeMessage neutralises it
		t.Errorf("body = %q", req.Body)
	}
	fc := h.only()
	if fc.Opts.Worker == nil || fc.Opts.Worker.Tests != closeout.TestsNone || fc.Opts.Worker.Hold != "" {
		t.Fatalf("worker = %+v", fc.Opts.Worker)
	}
	if got := strings.Join(fc.Opts.Worker.Reviewers, ","); got != "eng-review,license-review" {
		t.Errorf("reviewers = %s", got)
	}
	if len(fc.Opts.ExtraLogs) != 0 {
		t.Errorf("extra logs = %v", fc.Opts.ExtraLogs)
	}
	prs := h.prs()
	if len(prs) != 1 || !prs[0].Draft || prs[0].HeadRef != branch || prs[0].BaseRef != "main" || prs[0].Title != fmt.Sprintf("fix: Crash on start (#%d)", n) {
		t.Fatalf("prs = %+v", prs)
	}
	if strings.Count(prs[0].Body, "Closes #") != 1 || !strings.Contains(prs[0].Body, fmt.Sprintf("Closes #%d", n)) ||
		!strings.Contains(prs[0].Body, "Fixes issue 99") || strings.Contains(prs[0].Body, "Fixes #99") {
		t.Errorf("body:\n%s", prs[0].Body)
	}
	j, _ := h.store.Job(context.Background(), fc.JobID)
	if j.Branch != branch || j.PR != prs[0].Number || fc.Opts.Worker.PR != prs[0].Number {
		t.Errorf("job = %+v", j)
	}
	if !h.has(n, labels.Running(labels.StageWorker, "engineer")) {
		t.Error("no :running label")
	}
	if h.pool.offers != 1 {
		t.Errorf("offers = %d", h.pool.offers)
	}
	if _, held := h.pool.Lookup("worker:engineer:" + fmt.Sprint(n)); held {
		t.Error("slot still held")
	}
	// The agent's inputs are untrusted files outside .ard/<n>/.
	ej := h.ex.jobs[0]
	dir := fmt.Sprintf(".ard-input/%d/", n)
	var paths []string
	for _, f := range ej.Inputs {
		paths = append(paths, f.Path)
	}
	if strings.Join(paths, ",") != dir+"issue.md,"+dir+"comments.md" || len(ej.Scrub) != 1 || ej.Scrub[0] != agent.RoleFilePath(n, "worker", "engineer") || ej.Ref != h.cfg.SHA {
		t.Errorf("job = %+v", ej)
	}
}

func TestReviewersUnsetMeansAll(t *testing.T) {
	h := newHarness(t, noTests)
	h.cfg.Roles[0].Reviewers = nil
	h.issue("t")
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	if got := strings.Join(h.only().Opts.Worker.Reviewers, ","); got != "eng-review,license-review,sec-review" {
		t.Errorf("reviewers = %s", got)
	}
}

func TestTestsFailThenPass(t *testing.T) {
	h := newHarness(t)
	n := h.issue("Crash")
	var seen string
	tk := 0
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		switch kind {
		case "test":
			tk++
			if tk == 1 {
				writeFile(t, hd.Worktree, "unused", "")
				return 1
			}
			return 0
		case "fix":
			b, err := os.ReadFile(filepath.Join(hd.Worktree, fmt.Sprintf(".ard-input/%d/test-output.log", n)))
			if err != nil {
				t.Errorf("fix step has no test-output.log: %v", err)
			}
			seen = string(b)
			writeFile(t, hd.Worktree, "hello.txt", "hi")
		default:
			writeFile(t, hd.Worktree, "hello.txt", "bye")
		}
		writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", n), roleDone)
		return 0
	}
	h.mustTick()

	ids := h.ex.ids()
	if len(ids) != 4 {
		t.Fatalf("steps = %v", ids)
	}
	id := ids[0]
	if ids[1] != id+"t1" || ids[2] != id+"f1" || ids[3] != id+"t2" {
		t.Errorf("steps = %v", ids)
	}
	if h.code.n() != 2 || !h.code.reqs[0].Create || h.code.reqs[1].Create {
		t.Fatalf("code commits: %+v", h.code.reqs)
	}
	if h.code.reqs[1].Base != h.code.shas[0] || h.ex.jobs[1].Ref != h.code.shas[0] || h.ex.jobs[3].Ref != h.code.shas[1] || h.ex.jobs[2].Ref != h.code.shas[0] {
		t.Errorf("refs: %s %s %s / %s", h.ex.jobs[1].Ref, h.ex.jobs[2].Ref, h.ex.jobs[3].Ref, h.code.reqs[1].Base)
	}
	for _, want := range []string{"Command: sh check.sh", "Exit code: 1", "UNTRUSTED INPUT", "log of " + id + "t1"} {
		if !strings.Contains(seen, want) {
			t.Errorf("test-output.log lacks %q:\n%s", want, seen)
		}
	}
	tc := h.ex.cmds[id+"t1"]
	if tc.Path != "/bin/sh" || strings.Join(tc.Args, " ") != "-c sh check.sh" || len(tc.Env) != 0 {
		t.Errorf("test command = %+v", tc)
	}
	fc := h.only()
	if fc.H.JobID != id+"f1" || fc.Opts.Worker.Tests != closeout.TestsPassed || fc.Opts.Worker.Fixes != 1 || len(fc.Opts.ExtraLogs) != 1 {
		t.Errorf("finish = %+v %+v", fc.H.JobID, fc.Opts)
	}
	if fc.Opts.ExtraLogs[0] == fc.H.LogPath {
		t.Error("the final log is also an extra log")
	}
	clean := strings.Join(h.ex.clean, ",")
	for _, want := range []string{id, id + "t1", id + "t2"} {
		if !strings.Contains(clean, want) {
			t.Errorf("%s was not cleaned: %s", want, clean)
		}
	}
	if strings.Contains(clean, id+"f1") {
		t.Error("the final agent worktree was cleaned before close-out")
	}
	if len(h.prs()) != 1 {
		t.Error("no PR")
	}
}

func TestTestsFailThroughTheCap(t *testing.T) {
	h := newHarness(t)
	h.issue("Crash")
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, func(int) int { return 1 })
	h.mustTick()
	fc := h.only()
	w := fc.Opts.Worker
	if w.Tests != closeout.TestsFailed || w.Fixes != 2 || len(h.ex.ids()) != 6 || h.code.n() != 3 {
		t.Errorf("worker %+v, steps %d, commits %d", w, len(h.ex.ids()), h.code.n())
	}
	if len(h.prs()) != 1 {
		t.Error("a PR is still opened when the tests stay red")
	}
	if !strings.Contains(h.prs()[0].Body, "**Tests:** failed after 2 fix attempts") {
		t.Errorf("body:\n%s", h.prs()[0].Body)
	}
}

func TestFixThatChangesNothingStops(t *testing.T) {
	h := newHarness(t)
	n := h.issue("Crash")
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		switch kind {
		case "test":
			return 1
		case "agent":
			writeFile(t, hd.Worktree, "a.txt", "x")
		}
		writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", n), roleDone)
		return 0
	}
	h.mustTick()
	w := h.only().Opts.Worker
	if w.Tests != closeout.TestsFailed || w.Fixes != 1 || !strings.Contains(w.TestNote, "no files") || h.code.n() != 1 {
		t.Errorf("worker %+v, commits %d", w, h.code.n())
	}
}

func TestOutOfTime(t *testing.T) {
	h := newHarness(t)
	h.cfg.Roles[0].Timeout = time.Minute
	h.issue("Crash")
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, func(int) int { return 0 })
	h.mustTick()
	w := h.only().Opts.Worker
	if w.Tests != closeout.TestsFailed || w.TestNote != "ran out of time" || len(h.ex.ids()) != 1 {
		t.Errorf("worker %+v, steps %v", w, h.ex.ids())
	}
}

func TestGuardHolds(t *testing.T) {
	tok := "gh" + "s_" + strings.Repeat("aB3xZ9", 7)
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{"github dir", map[string]string{".github/CODEOWNERS": "* @x"}, []string{".github/CODEOWNERS"}},
		{"role dir", map[string]string{".ard/workers/engineer/prompt.txt": "x"}, []string{".ard/workers/engineer/prompt.txt"}},
		{"another issue's dir", map[string]string{".ard/999/plan.md": "x"}, []string{".ard/999/plan.md"}},
		{"secret-shaped file", map[string]string{"config/.env": "A=b"}, []string{"config/.env"}},
		{"binary", map[string]string{"x.bin": "a\x00b"}, []string{"x.bin: binary file"}},
		{"pattern names file line and rule, never text", map[string]string{"hello.txt": "ok\nkey = " + tok + "\n"}, []string{"hello.txt:2: secret scan rule github-token"}},
		{"known value", map[string]string{"x.txt": "v=" + tokenVal}, []string{"x.txt: contains the value of MODEL_KEY"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			n := h.issue("t")
			h.agentWrites(tc.files, roleDone, nil)
			h.mustTick()
			fc := h.only()
			if fc.Opts.Worker == nil || fc.Opts.Worker.Hold == "" {
				t.Fatalf("worker = %+v", fc.Opts.Worker)
			}
			for _, want := range tc.want {
				if !strings.Contains(fc.Opts.Worker.Hold, want) {
					t.Errorf("hold %q lacks %q", fc.Opts.Worker.Hold, want)
				}
			}
			if strings.Contains(fc.Opts.Worker.Hold, tok) || strings.Contains(fc.Opts.Worker.Hold, tokenVal) {
				t.Error("the hold quotes the credential")
			}
			if h.code.n() != 0 || len(h.prs()) != 0 {
				t.Error("something was pushed")
			}
			if j, _ := h.store.Job(context.Background(), fc.JobID); j.Branch != "" {
				t.Errorf("branch = %q", j.Branch)
			}
			_ = n
		})
	}
}

// A worker that edits a file that already holds a token-shaped fixture is not
// held for the lines it did not write; a new token-shaped literal is, and a
// fixture built at run time is not.
func TestOnlyAddedLinesAreScanned(t *testing.T) {
	tok := "AK" + "IA" + "QZ7K2M9TXW4HPN6B"
	run := func(t *testing.T, edit func(old string) string) string {
		h := newHarness(t, noTests)
		fixture := "package x\n\nvar k = \"" + tok + "\"\n"
		if err := os.WriteFile(filepath.Join(h.clone, "fixture_test.go"), []byte(fixture), 0o644); err != nil {
			t.Fatal(err)
		}
		gittest.Run(t, h.clone, "add", ".")
		gittest.Run(t, h.clone, "commit", "-m", "fixture")
		h.cfg.SHA = strings.TrimSpace(gittest.Run(t, h.clone, "rev-parse", "HEAD"))
		h.issue("t")
		h.agentWrites(map[string]string{"fixture_test.go": edit(fixture)}, roleDone, nil)
		h.mustTick()
		return h.only().Opts.Worker.Hold
	}
	t.Run("other lines of the file", func(t *testing.T) {
		if hold := run(t, func(old string) string { return old + "var other = 1\n" }); hold != "" {
			t.Errorf("hold = %q", hold)
		}
	})
	t.Run("a new literal", func(t *testing.T) {
		if hold := run(t, func(old string) string { return old + "var k2 = \"" + tok + "\"\n" }); !strings.Contains(hold, "fixture_test.go:4: secret scan rule") {
			t.Errorf("hold = %q", hold)
		}
	})
	t.Run("built at run time", func(t *testing.T) {
		if hold := run(t, func(old string) string {
			return old + "var k3 = \"AK\" + \"IA\" + \"QZ7K2M9TXW4HPN6B\"\n"
		}); hold != "" {
			t.Errorf("hold = %q", hold)
		}
	})
}

func TestAgentFailures(t *testing.T) {
	t.Run("exit 1", func(t *testing.T) {
		h := newHarness(t)
		h.issue("t")
		h.ex.run = func(string, executor.Job, executor.Handle) int { return 1 }
		h.mustTick()
		fc := h.only()
		if fc.Opts.Worker != nil || fc.St.ExitCode != 1 || h.code.n() != 0 || len(h.prs()) != 0 {
			t.Errorf("finish = %+v", fc)
		}
	})
	t.Run("needs-human role file", func(t *testing.T) {
		h := newHarness(t)
		h.issue("t")
		h.agentWrites(map[string]string{"a.txt": "x"}, roleFileOf("needs-human"), nil)
		h.mustTick()
		if fc := h.only(); fc.Opts.Worker != nil || h.code.n() != 0 {
			t.Errorf("finish = %+v, commits %d", fc, h.code.n())
		}
	})
	t.Run("no role file", func(t *testing.T) {
		h := newHarness(t)
		h.issue("t")
		h.agentWrites(map[string]string{"a.txt": "x"}, "", nil)
		h.mustTick()
		if fc := h.only(); fc.Opts.Worker != nil || h.code.n() != 0 {
			t.Errorf("finish = %+v", fc)
		}
	})
	t.Run("clone failure retries", func(t *testing.T) {
		h := newHarness(t)
		h.issue("t")
		h.ex.prepErr = fmt.Errorf("prepare: %w", executor.ErrClone)
		h.mustTick()
		c := h.only().Opts.Cause
		if c.Kind != closeout.CauseCloneFail || !c.Retry {
			t.Errorf("cause = %+v", c)
		}
	})
	t.Run("oversized inputs close out as inputs_too_large", func(t *testing.T) {
		h := newHarness(t)
		h.issue("t")
		h.ex.prepErr = fmt.Errorf("prepare: %w", executor.ErrInputsTooLarge)
		h.mustTick()
		c := h.only().Opts.Cause
		if c.Kind != closeout.CauseInputsTooLarge || c.Retry {
			t.Errorf("cause = %+v", c)
		}
	})
	t.Run("other prepare failure does not retry", func(t *testing.T) {
		h := newHarness(t)
		h.issue("t")
		h.ex.prepErr = errBoom
		h.mustTick()
		c := h.only().Opts.Cause
		if c.Kind != closeout.CauseNoProcess || c.Retry {
			t.Errorf("cause = %+v", c)
		}
	})
}

func TestPushFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"branch exists": {committer.ErrBranchExists, "branch exists"},
		"conflict":      {committer.ErrConflict, "conflict"},
		"branch gone":   {committer.ErrBranchGone, "branch gone"},
		"guard":         {&committer.GuardError{Paths: []string{"x"}, Reasons: []string{"y"}}, "guard"},
		"other":         {errBoom, "push"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, noTests)
			h.issue("t")
			h.code.err = tc.err
			h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
			h.mustTick()
			c := h.only().Opts.Cause
			if c.Kind != closeout.CauseNoProcess || c.Retry || c.Detail != "could not push the branch: "+tc.want {
				t.Errorf("cause = %+v", c)
			}
			if len(h.prs()) != 0 {
				t.Error("a PR without a branch")
			}
		})
	}
}

func TestCreatePRFailure(t *testing.T) {
	h := newHarness(t, noTests)
	h.issue("t")
	h.gh.FailNext("CreatePR", errBoom)
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	fc := h.only()
	// The branch was pushed, so the failure is the retryable-by-label kind.
	if fc.Opts.Cause.Kind != closeout.CausePRCreateFailed || fc.Opts.Cause.Retry || fc.Opts.Cause.Transient {
		t.Errorf("cause = %+v", fc.Opts.Cause)
	}
}

func TestCreatePRFailureNoPush(t *testing.T) {
	h := newHarness(t, noTests)
	n := h.issue("t")
	br := fmt.Sprintf("worker/engineer-%d-t", n)
	gittest.Run(t, h.clone, "push", "origin", h.code.shas[0]+":refs/heads/"+br)
	h.gh.FailNext("CreatePR", errBoom)
	h.agentWrites(nil, roleDone, nil)
	h.mustTick()
	fc := h.only()
	if fc.Opts.Cause.Kind != closeout.CauseNoProcess || fc.Opts.Cause.Detail != "could not open the pull request" {
		t.Errorf("cause = %+v", fc.Opts.Cause)
	}
}

func http5xx() error { return &github.HTTPError{Status: 502} }

func TestCreatePRRetriesThenOpens(t *testing.T) {
	h := newHarness(t, noTests)
	n := h.issue("t")
	var sleeps int
	h.loop.d.Sleep = func(context.Context, time.Duration) { sleeps++ }
	h.gh.FailNext("CreatePR", http5xx())
	h.gh.FailNext("CreatePR", http5xx())
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	fc := h.only()
	if fc.Opts.Cause.Kind != "" || fc.Opts.Worker == nil || fc.Opts.Worker.PR == 0 {
		t.Errorf("opts = %+v", fc.Opts)
	}
	if len(h.prs()) != 1 || len(h.ex.jobs) != 1 || sleeps != 2 || h.has(n, labels.ExecutorFailed) {
		t.Errorf("prs=%d agent steps=%d sleeps=%d failed=%v", len(h.prs()), len(h.ex.jobs), sleeps, h.has(n, labels.ExecutorFailed))
	}
}

func TestCreatePRNonTransientNotRetried(t *testing.T) {
	h := newHarness(t, noTests)
	h.issue("t")
	h.gh.FailNext("CreatePR", &github.HTTPError{Status: 422})
	h.gh.FailNext("CreatePR", &github.HTTPError{Status: 422})
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	if fc := h.only(); fc.Opts.Cause.Kind != closeout.CausePRCreateFailed {
		t.Errorf("cause = %+v", fc.Opts.Cause)
	}
	if len(h.prs()) != 0 {
		t.Errorf("a PR was opened")
	}
}

func TestCreatePRSleepStopsOnCancel(t *testing.T) {
	h := newHarness(t, noTests)
	h.loop.d.Sleep = nil // the real timer
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	jb := &job{l: h.loop, log: quiet}
	if err := jb.sleep(ctx, time.Hour); err == nil {
		t.Error("sleep ignored a cancelled context")
	}
}

// failedPush runs a job whose CreatePR always fails after the push and returns
// the issue, the branch and the recorded head, ready for the label removal.
func failedPush(t *testing.T, h *harness) (int, string, string) {
	t.Helper()
	n := h.issue("t")
	for i := 0; i < 4; i++ {
		h.gh.FailNext("CreatePR", http5xx())
	}
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	fc := h.only()
	if fc.Opts.Cause.Kind != closeout.CausePRCreateFailed || fc.Opts.Cause.Retry {
		t.Fatalf("cause = %+v", fc.Opts.Cause)
	}
	if !strings.Contains(fc.Opts.Cause.Detail, "worker/engineer-") && !strings.Contains(fc.Opts.Cause.Detail, h.code.shas[0][:12]) {
		t.Errorf("detail = %q", fc.Opts.Cause.Detail)
	}
	j, err := h.store.Job(context.Background(), fc.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.PendingPR) == 0 {
		t.Fatal("no pending_pr recorded")
	}
	var pp state.PendingPR
	if err := json.Unmarshal(j.PendingPR, &pp); err != nil || pp.Head != h.code.shas[0] || pp.Branch != j.Branch || pp.Body == "" {
		t.Fatalf("pending = %+v err %v", pp, err)
	}
	// Close the job like close-out would, and free the issue for a re-run.
	if err := h.store.Fail(context.Background(), j.ID, "pr_create_failed", t0); err != nil {
		t.Fatal(err)
	}
	if err := h.gh.RemoveLabel(context.Background(), n, labels.Running(labels.StageWorker, "engineer")); err != nil {
		t.Fatal(err)
	}
	return n, j.Branch, pp.Head
}

func TestReopenPRWithoutAgent(t *testing.T) {
	h := newHarness(t, noTests)
	_, br, head := failedPush(t, h)
	gittest.Run(t, h.clone, "push", "origin", head+":refs/heads/"+br)
	h.mustTick()
	got := h.fin.got()
	if len(got) != 2 {
		t.Fatalf("FinishWith calls = %d", len(got))
	}
	fc := got[1]
	if fc.Opts.Cause.Kind != "" || fc.Opts.Worker == nil || !fc.Opts.Worker.Reopened || fc.Opts.Worker.PR == 0 || !fc.Opts.Worker.Pushed {
		t.Errorf("opts = %+v worker %+v", fc.Opts, fc.Opts.Worker)
	}
	if len(h.ex.jobs) != 1 || h.code.n() != 1 {
		t.Errorf("agent steps = %d, code commits = %d, want 1 and 1", len(h.ex.jobs), h.code.n())
	}
	prs := h.prs()
	if len(prs) != 1 || prs[0].HeadRef != br || !strings.Contains(prs[0].Body, "Closes #") {
		t.Errorf("prs = %+v", prs)
	}
}

func TestReopenPRTolerantOfCloseoutCommit(t *testing.T) {
	h := newHarness(t, noTests)
	_, br, head := failedPush(t, h)
	gittest.Run(t, h.clone, "push", "origin", head+":refs/heads/"+br)
	// Close-out committed the log under .ard/ on top of the head.
	gittest.Run(t, h.clone, "checkout", "-q", "--detach", head)
	if err := os.MkdirAll(filepath.Join(h.clone, ".ard", "1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.clone, ".ard", "1", "usage.md"), []byte("u"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, h.clone, "add", ".ard")
	gittest.Run(t, h.clone, "commit", "-q", "-m", "log")
	gittest.Run(t, h.clone, "push", "-f", "origin", "HEAD:refs/heads/"+br)
	h.mustTick()
	if got := h.fin.got(); len(got) != 2 || !got[1].Opts.Worker.Reopened || len(h.ex.jobs) != 1 {
		t.Errorf("finish calls = %d, agent steps = %d", len(got), len(h.ex.jobs))
	}
}

func TestReopenPRBranchMoved(t *testing.T) {
	h := newHarness(t, noTests)
	h.code.shas[1] = h.commitFiles(map[string]string{"a.txt": "y"})
	_, br, _ := failedPush(t, h)
	gittest.Run(t, h.clone, "push", "origin", h.code.shas[1]+":refs/heads/"+br)
	h.agentWrites(map[string]string{"a.txt": "z"}, roleDone, nil)
	h.mustTick()
	if len(h.ex.jobs) < 2 {
		t.Errorf("agent steps = %d, want the full worker to run", len(h.ex.jobs))
	}
}

func TestReopenPRAlreadyExists(t *testing.T) {
	h := newHarness(t, noTests)
	_, br, head := failedPush(t, h)
	gittest.Run(t, h.clone, "push", "origin", head+":refs/heads/"+br)
	h.gh.SeedPR(github.PullRequest{Title: "x", HeadRef: br, BaseRef: "main", State: github.StateOpen, Draft: true})
	h.agentWrites(nil, roleDone, nil)
	h.mustTick()
	if len(h.ex.jobs) < 2 {
		t.Errorf("agent steps = %d, want the full run", len(h.ex.jobs))
	}
	if len(h.prs()) != 1 {
		t.Errorf("prs = %d, want no duplicate", len(h.prs()))
	}
}

func TestCreatePRDryRunDoesNotRetry(t *testing.T) {
	h := newHarness(t, noTests, dryRun)
	h.issue("t")
	var sleeps int
	h.loop.d.Sleep = func(context.Context, time.Duration) { sleeps++ }
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	if sleeps != 0 {
		t.Errorf("slept %d times under dry-run", sleeps)
	}
}

func TestExistingBranchAndPR(t *testing.T) {
	h := newHarness(t, noTests)
	n := h.issue("A new title")
	old := fmt.Sprintf("worker/engineer-%d-old-title", n)
	gittest.Run(t, h.clone, "push", "origin", h.code.shas[0]+":refs/heads/"+old)
	prn := h.gh.SeedPR(github.PullRequest{Number: 0, Title: "old", HeadRef: old, BaseRef: "main", State: github.StateOpen, Draft: true})
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()

	if h.code.n() != 1 || h.code.reqs[0].Create || h.code.br[0] != old || h.code.reqs[0].Base != h.code.shas[0] {
		t.Fatalf("commits: %+v on %v", h.code.reqs, h.code.br)
	}
	if h.ex.jobs[0].Ref != "refs/ard/"+old {
		t.Errorf("agent ref = %s", h.ex.jobs[0].Ref)
	}
	prs := h.prs()
	if len(prs) != 1 || prs[0].Number != prn || !strings.Contains(prs[0].Title, "A new title") || !strings.Contains(prs[0].Body, "Closes #") {
		t.Errorf("prs = %+v", prs)
	}
	fc := h.only()
	if j, _ := h.store.Job(context.Background(), fc.JobID); j.Branch != old || j.PR != prn {
		t.Errorf("job = %+v", j)
	}
}

func TestExistingBranchWithNoNewCodeGoesToTests(t *testing.T) {
	h := newHarness(t)
	n := h.issue("t")
	br := fmt.Sprintf("worker/engineer-%d-x", n)
	gittest.Run(t, h.clone, "push", "origin", h.code.shas[0]+":refs/heads/"+br)
	h.agentWrites(nil, roleDone, func(int) int { return 0 })
	h.mustTick()
	if h.code.n() != 0 {
		t.Error("pushed with nothing to push")
	}
	ids := h.ex.ids()
	if len(ids) != 2 || h.ex.jobs[1].Ref != h.code.shas[0] {
		t.Errorf("steps %v, test ref %s", ids, h.ex.jobs[1].Ref)
	}
	if w := h.only().Opts.Worker; w.Tests != closeout.TestsPassed || w.PR == 0 {
		t.Errorf("worker = %+v", w)
	}
}

func TestAmbiguousBranches(t *testing.T) {
	h := newHarness(t)
	n := h.issue("t")
	for _, s := range []string{"a", "b"} {
		gittest.Run(t, h.clone, "push", "origin", h.code.shas[0]+fmt.Sprintf(":refs/heads/worker/engineer-%d-%s", n, s))
	}
	h.mustTick()
	h.mustTick()
	if len(h.ex.ids()) != 0 || len(h.jobs()) != 0 || len(h.fin.got()) != 0 {
		t.Error("a job was claimed")
	}
	if !h.has(n, labels.NeedsHuman) {
		t.Error("no needs-human")
	}
	if cs := h.comments(n); len(cs) != 1 || !strings.HasPrefix(cs[0].Body, "<!-- ard:worker branches -->") {
		t.Errorf("comments = %+v", cs)
	}
}

func TestShutdownLeavesJobRunning(t *testing.T) {
	h := newHarness(t)
	n := h.issue("t")
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.ex.status = func(string) (executor.Status, bool) {
		return executor.Status{State: executor.Running, Alive: true}, true
	}
	in, err := h.tickAsync()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for len(h.jobs()) == 0 || len(h.ex.ids()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("job did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(h.stop)
	select {
	case <-in.Wait():
	case <-time.After(10 * time.Second):
		t.Fatal("watcher did not stop")
	}
	if len(h.fin.got()) != 0 || len(h.jobs()) != 1 || !h.has(n, labels.Running(labels.StageWorker, "engineer")) {
		t.Error("a shutdown must leave the job running for adopt and the Reaper")
	}
}

func TestKillDuringTests(t *testing.T) {
	h := newHarness(t)
	h.issue("t")
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, func(int) int { return 0 })
	h.ex.status = func(id string) (executor.Status, bool) {
		if kindOf(id) == "test" {
			return executor.Status{State: executor.Gone}, true
		}
		return executor.Status{}, false
	}
	h.mustTick()
	fc := h.only()
	// Close-out reads the agent step that made the code, not the test worktree,
	// and names why the test step ended.
	if kindOf(fc.H.JobID) != "agent" || fc.Opts.Cause.Kind == "" || len(fc.Opts.ExtraLogs) != 0 {
		t.Errorf("finish = %+v", fc)
	}
	// The pushed branch still gets its draft PR.
	if len(h.prs()) != 1 || fc.Opts.Worker == nil || fc.Opts.Worker.PR == 0 || !fc.Opts.Worker.Pushed {
		t.Errorf("prs %d worker %+v", len(h.prs()), fc.Opts.Worker)
	}
}

func TestBudgetCapsTheWholeJob(t *testing.T) {
	h := newHarness(t)
	h.cfg.Roles[0].MaxBudgetUSD = 1
	h.issue("t")
	h.loop.d.Parse = func(context.Context, *config.Resolved, state.Job, executor.Handle, int) (agent.Result, error) {
		c := 1.0
		return agent.Result{CostUSD: &c}, nil
	}
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, func(int) int { return 1 })
	h.mustTick()
	if args := strings.Join(h.ex.cmds[h.ex.ids()[0]].Args, " "); !strings.Contains(args, "--max-budget-usd=1") {
		t.Errorf("first step args: %s", args)
	}
	fc := h.only()
	if !strings.Contains(fc.Opts.Cause.Detail, "budget exhausted") || len(h.ex.ids()) != 2 {
		t.Errorf("cause %+v, steps %v", fc.Opts.Cause, h.ex.ids())
	}
	if fc.Res.CostUSD == nil || *fc.Res.CostUSD != 1 {
		t.Errorf("cost = %v", fc.Res.CostUSD)
	}
	// Pushed work is never left without a PR.
	if len(h.prs()) != 1 || fc.Opts.Worker == nil || fc.Opts.Worker.PR == 0 {
		t.Errorf("prs %d worker %+v", len(h.prs()), fc.Opts.Worker)
	}
}

func TestBudgetShrinksForEachStep(t *testing.T) {
	h := newHarness(t)
	h.cfg.Roles[0].MaxBudgetUSD = 2
	h.issue("t")
	h.loop.d.Parse = func(context.Context, *config.Resolved, state.Job, executor.Handle, int) (agent.Result, error) {
		c := 0.5
		return agent.Result{CostUSD: &c}, nil
	}
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, func(k int) int { // red then green
		if k == 1 {
			return 1
		}
		return 0
	})
	h.mustTick()
	ids := h.ex.ids()
	a0 := strings.Join(h.ex.cmds[ids[0]].Args, " ")
	a1 := strings.Join(h.ex.cmds[ids[2]].Args, " ")
	if !strings.Contains(a0, "--max-budget-usd=2") || !strings.Contains(a1, "--max-budget-usd=1.5") {
		t.Errorf("budgets:\n%s\n%s", a0, a1)
	}
}

func TestFixRoundNeedsHumanStillOpensThePR(t *testing.T) {
	h := newHarness(t)
	n := h.issue("t")
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		switch kind {
		case "test":
			return 1
		case "agent":
			writeFile(t, hd.Worktree, "a.txt", "x")
			writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", n), roleDone)
		case "fix":
			writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", n), roleFileOf("needs-human"))
		}
		return 0
	}
	h.mustTick()
	fc := h.only()
	if len(h.prs()) != 1 || fc.Opts.Worker == nil || fc.Opts.Worker.PR == 0 || fc.Opts.Worker.Hold != "" {
		t.Errorf("prs %d worker %+v", len(h.prs()), fc.Opts.Worker)
	}
	if kindOf(fc.H.JobID) != "fix" {
		t.Errorf("closed out through %s", fc.H.JobID)
	}
}

func TestFixRoundGuardHoldStillOpensThePR(t *testing.T) {
	h := newHarness(t)
	n := h.issue("t")
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		switch kind {
		case "test":
			return 1
		case "fix":
			writeFile(t, hd.Worktree, ".github/CODEOWNERS", "x")
		default:
			writeFile(t, hd.Worktree, "a.txt", "x")
		}
		writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", n), roleDone)
		return 0
	}
	h.mustTick()
	w := h.only().Opts.Worker
	if len(h.prs()) != 1 || w == nil || !strings.Contains(w.Hold, ".github/CODEOWNERS") || w.PR == 0 {
		t.Errorf("prs %d worker %+v", len(h.prs()), w)
	}
}

func TestPlanIsAnInputOnlyIfItExists(t *testing.T) {
	for _, have := range []bool{true, false} {
		t.Run(fmt.Sprint(have), func(t *testing.T) {
			h := newHarness(t, noTests)
			n := h.issue("t")
			if have {
				if err := os.MkdirAll(filepath.Join(h.clone, ".ard", fmt.Sprint(n)), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(h.clone, ".ard", fmt.Sprint(n), "plan.md"), []byte("## Plan\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				gittest.Run(t, h.clone, "add", ".")
				gittest.Run(t, h.clone, "commit", "-m", "plan")
				h.cfg.SHA = strings.TrimSpace(gittest.Run(t, h.clone, "rev-parse", "HEAD"))
			}
			h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
			h.mustTick()
			args := strings.Join(h.ex.cmds[h.ex.ids()[0]].Args, " ")
			if got := strings.Contains(args, fmt.Sprintf(".ard/%d/plan.md", n)); got != have {
				t.Errorf("plan in args = %v, want %v:\n%s", got, have, args)
			}
		})
	}
}

// The agent controls its worktree's index and .git: neither may hide a secret.
func TestGuardIgnoresTheAgentsIndexAndGitdir(t *testing.T) {
	tok := "gh" + "s_" + strings.Repeat("aB3xZ9", 7)
	git := func(wt string, args ...string) {
		t.Helper()
		gittest.Run(t, wt, args...)
	}
	for name, attack := range map[string]func(wt string){
		"skip-worktree": func(wt string) {
			writeFile(t, wt, "hello.txt", "harmless\n")
			git(wt, "add", "hello.txt")
			git(wt, "update-index", "--skip-worktree", "hello.txt")
			writeFile(t, wt, "hello.txt", "key = "+tok+"\n")
		},
		"assume-unchanged": func(wt string) {
			writeFile(t, wt, "hello.txt", "harmless\n")
			git(wt, "add", "hello.txt")
			git(wt, "update-index", "--assume-unchanged", "hello.txt")
			writeFile(t, wt, "hello.txt", "key = "+tok+"\n")
		},
		"header lookalike line": func(wt string) {
			writeFile(t, wt, "a.txt", "x\n++ b/zzz\ny\nkey = "+tok+"\n")
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, noTests)
			h.issue("t")
			h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
				attack(hd.Worktree)
				writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", j.Issue), roleDone)
				return 0
			}
			h.mustTick()
			w := h.only().Opts.Worker
			if w == nil || !strings.Contains(w.Hold, "secret scan rule github-token") || h.code.n() != 0 {
				t.Errorf("worker %+v, commits %d", w, h.code.n())
			}
			if w != nil && strings.Contains(w.Hold, tok) {
				t.Error("hold quotes the credential")
			}
		})
	}
}

func TestOverMaxLinesCannotBeHiddenFromTheCount(t *testing.T) {
	h := newHarness(t, noTests)
	h.issue("t")
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		writeFile(t, hd.Worktree, "big.txt", "x\n")
		gittest.Run(t, hd.Worktree, "add", "big.txt")
		gittest.Run(t, hd.Worktree, "update-index", "--skip-worktree", "big.txt")
		writeFile(t, hd.Worktree, "big.txt", strings.Repeat("x\n", 2500))
		writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", j.Issue), roleDone)
		return 0
	}
	h.mustTick()
	if w := h.only().Opts.Worker; w == nil || !strings.Contains(w.Hold, "changed lines") {
		t.Errorf("worker = %+v", w)
	}
}

func TestDiffLineThresholds(t *testing.T) {
	for _, tc := range []struct {
		name      string
		warn, max int
		lines     int
		hold      string // "" = a PR opens
		note      string // "" = no large-change note
	}{
		{"defaults, under warn", 1000, 2000, 900, "", ""},
		{"defaults, 1200 lines warn", 1000, 2000, 1200, "", "Large change: 1200 changed lines (warn at 1000)"},
		{"defaults, 2100 lines hold", 1000, 2000, 2100, "diff guard: 2100 changed lines, limit 2000", ""},
		{"role override raises the limit", 1500, 3000, 2100, "", "Large change: 2100 changed lines (warn at 1500)"},
		{"role override lowers the limit", 100, 200, 300, "diff guard: 300 changed lines, limit 200", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, noTests)
			h.cfg.Roles[0].DiffWarnLines, h.cfg.Roles[0].DiffMaxLines = tc.warn, tc.max
			h.issue("t")
			h.agentWrites(map[string]string{"big.txt": strings.Repeat("x\n", tc.lines)}, roleDone, nil)
			h.mustTick()
			w := h.only().Opts.Worker
			if w == nil {
				t.Fatal("no worker result")
			}
			if w.Hold != tc.hold {
				t.Errorf("hold = %q, want %q", w.Hold, tc.hold)
			}
			if tc.hold != "" {
				if len(h.prs()) != 0 {
					t.Error("a held job opened a PR")
				}
				return
			}
			if len(h.prs()) != 1 {
				t.Fatalf("PRs = %d, want 1", len(h.prs()))
			}
			if w.Large != tc.note {
				t.Errorf("large = %q, want %q", w.Large, tc.note)
			}
			body := h.prs()[0].Body
			if tc.note != "" && !strings.Contains(body, "**"+tc.note+"**") {
				t.Errorf("body lacks the note:\n%s", body)
			}
			if tc.note == "" && strings.Contains(body, "Large change") {
				t.Errorf("body has a note under the warn threshold:\n%s", body)
			}
		})
	}
}

func TestTriageOutputsAreUntrustedInputs(t *testing.T) {
	h := newHarness(t, noTests)
	n := h.issue("t")
	dir := filepath.Join(h.clone, ".ard", fmt.Sprint(n))
	for rel, body := range map[string]string{
		"plan.md": "## Plan\n", "triage-lead.md": "x", "triage-architect.md": "x", "worker-engineer.md": "old", "issue.md": "y",
		"logs/triage-lead-20260101T000000Z.log": "log", "logs/worker-engineer-20260101T000000Z.log": "log",
	} {
		writeFile(t, dir, rel, body)
	}
	gittest.Run(t, h.clone, "add", ".")
	gittest.Run(t, h.clone, "commit", "-m", "triage")
	h.cfg.SHA = strings.TrimSpace(gittest.Run(t, h.clone, "rev-parse", "HEAD"))
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	args := strings.Join(h.ex.cmds[h.ex.ids()[0]].Args, " ")
	p := fmt.Sprintf(".ard/%d/", n)
	for _, want := range []string{p + "plan.md", p + "issue.md", p + "triage-lead.md", p + "triage-architect.md", p + "logs/triage-lead-20260101T000000Z.log"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %s:\n%s", want, args)
		}
	}
	for _, not := range []string{"worker-engineer-2026"} {
		if strings.Contains(args, not) {
			t.Errorf("args wrongly name %s", not)
		}
	}
}

type failingBranchStore struct{ Store }

func (failingBranchStore) SetBranch(context.Context, string, string) error { return errBoom }

func TestSetBranchFailureStillOpensThePR(t *testing.T) {
	h := newHarness(t, noTests)
	h.issue("t")
	h.loop.d.Store = failingBranchStore{h.loop.d.Store}
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	fc := h.only()
	if len(h.prs()) != 1 || fc.Opts.Cause.Detail != "could not record the branch" || fc.Opts.Worker == nil || fc.Opts.Worker.PR == 0 {
		t.Errorf("prs %d finish %+v", len(h.prs()), fc.Opts)
	}
}

// What is committed is what was scanned, even if a surviving process rewrites
// the file in the worktree between the scan and the commit.
func TestCommittedBytesAreTheScannedBytes(t *testing.T) {
	h := newHarness(t, noTests)
	h.issue("t")
	var wt string
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		wt = hd.Worktree
		writeFile(t, hd.Worktree, "a.txt", "scanned\n")
		writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", j.Issue), roleDone)
		return 0
	}
	h.code.before = func() {
		writeFile(t, wt, "a.txt", "key = "+"gh"+"s_"+strings.Repeat("aB3xZ9", 7)+"\n")
	}
	h.mustTick()
	if h.code.n() != 1 || h.code.files[0]["a.txt"] != "scanned\n" {
		t.Errorf("committed %v", h.code.files)
	}
}

func TestSizeAndCountLimitsHoldBeforeHashing(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  string
	}{
		"a file over 1 MiB": {map[string]string{"big.txt": strings.Repeat("x", MaxFileSize+1)}, "big.txt: larger than the file size limit"},
		"257 new files": {func() map[string]string {
			m := map[string]string{}
			for i := 0; i <= MaxFiles; i++ {
				m[fmt.Sprintf("d/f%03d.txt", i)] = "x\n"
			}
			return m
		}(), "more than 256 new or resized files"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, noTests)
			h.issue("t")
			h.agentWrites(tc.files, roleDone, nil)
			h.mustTick()
			w := h.only().Opts.Worker
			if w == nil || !strings.Contains(w.Hold, "diff guard: ") || !strings.Contains(w.Hold, tc.want) || h.code.n() != 0 {
				t.Errorf("worker %+v", w)
			}
		})
	}
}

// Each agent and fix step is recorded under its own key, and the job's totals
// in state are their sum. The Finisher is handed the final step only: it reads
// the totals from state, so nothing is counted twice.
func TestStepUsageRecordedForEveryAgentStep(t *testing.T) {
	h := newHarness(t)
	h.issue("Crash")
	h.loop.d.Parse = func(_ context.Context, _ *config.Resolved, _ state.Job, hd executor.Handle, _ int) (agent.Result, error) {
		c := 0.25
		u := agent.Usage{Input: 10, Output: 100, CacheWrite: 1000, CacheRead: 10000}
		return agent.Result{CostUSD: &c, Usage: &u, Model: "claude-opus-5-5", ModelUsage: map[string]agent.Usage{"claude-opus-5-5": u}}, nil
	}
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, func(int) int { return 1 })
	h.mustTick()
	fc := h.only()
	id := h.ex.ids()[0]
	if fc.JobID != id {
		t.Fatalf("finished job %s, want %s", fc.JobID, id)
	}
	u, err := h.store.JobUsage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	// An agent step and two fix steps; the test steps run no agent.
	if u.Steps != 3 || u.Tokens == nil || *u.Tokens != (state.Tokens{Input: 30, Output: 300, CacheWrite: 3000, CacheRead: 30000}) ||
		u.CostUSD == nil || *u.CostUSD != 0.75 || len(u.Models) != 1 {
		t.Errorf("usage = %+v tokens=%+v", u, u.Tokens)
	}
	if fc.Res.CostUSD == nil || *fc.Res.CostUSD != 0.25 {
		t.Errorf("the Finisher was handed the job sum %v instead of the final step's own result", fc.Res.CostUSD)
	}
	// Re-recording at finish (or a replay) does not change the sums.
	if rec, err := h.store.RecordStepUsage(context.Background(), id, id, state.StepUsage{CostUSD: new(float64)}); err != nil || rec {
		t.Errorf("re-record = %v, %v", rec, err)
	}
}

// commitFiles makes a commit on top of the config commit that adds files and
// returns its SHA, leaving the clone's HEAD where it was.
func (h *harness) commitFiles(files map[string]string) string {
	h.t.Helper()
	for p, c := range files {
		writeFile(h.t, h.clone, p, c)
	}
	gittest.Run(h.t, h.clone, "add", "-A")
	gittest.Run(h.t, h.clone, "commit", "-m", "files")
	sha := strings.TrimSpace(gittest.Run(h.t, h.clone, "rev-parse", "HEAD"))
	gittest.Run(h.t, h.clone, "reset", "--hard", h.cfg.SHA)
	return sha
}

func TestReviewersWhenRules(t *testing.T) {
	goMod := &config.When{Paths: []string{"**/go.mod"}}
	arch := &config.When{Triage: []string{"architect"}}
	tests := []struct {
		name   string
		files  map[string]string
		labels []string
		list   []string // the worker's reviewers: list
		when   map[string]*config.When
		want   string
	}{
		{"gated reviewer skipped", map[string]string{"a.txt": "x"}, nil, nil,
			map[string]*config.When{"license-review": goMod}, "eng-review,sec-review"},
		{"gated reviewer included on path hit", map[string]string{"go.mod": "module x\n"}, nil, nil,
			map[string]*config.When{"license-review": goMod}, "eng-review,license-review,sec-review"},
		{"triage label counts", map[string]string{"a.txt": "x"}, []string{labels.Done(labels.StageTriage, "architect")}, nil,
			map[string]*config.When{"license-review": arch}, "eng-review,license-review,sec-review"},
		{"triage running does not count", map[string]string{"a.txt": "x"}, []string{labels.Running(labels.StageTriage, "architect")}, nil,
			map[string]*config.When{"license-review": arch}, "eng-review,sec-review"},
		{"worker list does not bypass when", map[string]string{"a.txt": "x"}, nil, []string{"eng-review", "license-review"},
			map[string]*config.When{"license-review": goMod}, "eng-review"},
		{"worker list narrows", map[string]string{"go.mod": "module x\n"}, nil, []string{"license-review"},
			map[string]*config.When{"license-review": goMod}, "license-review"},
		{"ard tree is not selectable", map[string]string{".ard/notes.md": "x", "a.txt": "x"}, nil, nil,
			map[string]*config.When{"license-review": {Paths: []string{".ard/"}}}, "eng-review,sec-review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, noTests)
			h.cfg.Roles[0].Reviewers = tt.list
			h.cfg.Roles = append(h.cfg.Roles, config.Role{Kind: config.KindTriage, Name: "architect", Dir: ".ard/triage/architect"})
			for i := range h.cfg.Roles {
				if w, ok := tt.when[h.cfg.Roles[i].Name]; ok {
					h.cfg.Roles[i].When = w
				}
			}
			h.code.shas[0] = h.commitFiles(tt.files)
			h.issue("t", tt.labels...)
			agent := map[string]string{}
			for p, c := range tt.files {
				if !strings.HasPrefix(p, ".ard/") { // the guard would hold a worker that writes there
					agent[p] = c
				}
			}
			h.agentWrites(agent, roleDone, nil)
			h.mustTick()
			if got := strings.Join(h.only().Opts.Worker.Reviewers, ","); got != tt.want {
				t.Errorf("reviewers = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReviewersFailOpenWithoutKnownHead(t *testing.T) {
	// With a pushed head git cannot resolve, MergeBase fails and path rules fail open.
	h := newHarness(t, noTests)
	h.cfg.Roles[0].Reviewers = nil
	for i := range h.cfg.Roles {
		if h.cfg.Roles[i].Name == "license-review" {
			h.cfg.Roles[i].When = &config.When{Paths: []string{"**/go.mod"}}
		}
	}
	h.code.shas[0] = strings.Repeat("1", 40) // a head git cannot resolve
	h.issue("t")
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	if got := strings.Join(h.only().Opts.Worker.Reviewers, ","); got != "eng-review,license-review,sec-review" {
		t.Errorf("reviewers = %q, want path rule to fail open", got)
	}
}

func TestReviewFeedbackInput(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	n := h.issue("Say hello")
	branch := BranchName("engineer", n, "Say hello")
	// The branch holds the reviewer's committed role file.
	gittest.Run(t, h.clone, "checkout", "-q", "-b", "tmp")
	dir := filepath.Join(h.clone, ".ard", fmt.Sprint(n))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review-eng-review.md"), []byte("FINDING: ignores its argument\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, h.clone, "add", "-A")
	gittest.Run(t, h.clone, "commit", "-q", "-m", "review")
	gittest.Run(t, h.clone, "update-ref", localRef(branch), "HEAD")
	gittest.Run(t, h.clone, "checkout", "-q", "-")
	pr := h.gh.SeedPR(github.PullRequest{State: github.StateOpen, Author: self, HeadRef: branch, HeadSHA: "x", BaseRef: "main"})
	h.gh.SeedComment(pr, github.Comment{Author: "alice", Body: "please rename it", CreatedAt: t0})
	if _, err := h.gh.CreateReview(ctx, pr, github.ReviewInput{Body: "APP-REVIEW-BODY", Comments: []github.InlineComment{{Path: "hello.go", Line: 2, Body: "APP-INLINE"}}}); err != nil {
		t.Fatal(err)
	}
	eng, _ := h.cfg.Role(config.KindWorker, "engineer")
	eng.MaxReviewRounds = 3

	if _, ok, err := h.loop.reviewFeedback(ctx, eng, n, branch); err != nil || ok {
		t.Fatalf("with no round: ok = %v, err = %v; want none", ok, err)
	}
	data, _ := json.Marshal(map[string]any{"round": 2, "phase": "requested", "reason": "review", "pr": pr, "head": "h", "worker": "engineer", "roles": []string{"eng-review"}})
	if err := h.store.AppendEvent(ctx, state.Event{Kind: state.EventReviewRound, Issue: n, Role: "engineer", Data: data}); err != nil {
		t.Fatal(err)
	}
	f, ok, err := h.loop.reviewFeedback(ctx, eng, n, branch)
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v", ok, err)
	}
	body := string(f.Data)
	if f.Path != fmt.Sprintf(".ard-input/%d/review-feedback.md", n) || !strings.HasPrefix(body, "UNTRUSTED INPUT") {
		t.Errorf("path %q, body:\n%s", f.Path, body)
	}
	for _, want := range []string{"Review round 2 of 3", "### review-eng-review.md", "FINDING: ignores its argument", "alice", "please rename it"} {
		if !strings.Contains(body, want) {
			t.Errorf("feedback lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "APP-INLINE") || strings.Contains(body, "APP-REVIEW-BODY") {
		t.Errorf("the App's own comments are included:\n%s", body)
	}
}

func TestReviewFeedbackOversizeRoleFile(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	n := h.issue("Say hello")
	branch := BranchName("engineer", n, "Say hello")
	// A committed role file may be up to closeout.MaxRoleFileBytes.
	gittest.Run(t, h.clone, "checkout", "-q", "-b", "tmp")
	dir := filepath.Join(h.clone, ".ard", fmt.Sprint(n))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "review-eng-review.md"), []byte(strings.Repeat("x", 200<<10)), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, h.clone, "add", "-A")
	gittest.Run(t, h.clone, "commit", "-q", "-m", "review")
	gittest.Run(t, h.clone, "update-ref", localRef(branch), "HEAD")
	gittest.Run(t, h.clone, "checkout", "-q", "-")
	pr := h.gh.SeedPR(github.PullRequest{State: github.StateOpen, Author: self, HeadRef: branch, HeadSHA: "x", BaseRef: "main"})
	data, _ := json.Marshal(map[string]any{"round": 1, "phase": "requested", "reason": "review", "pr": pr, "head": "h", "worker": "engineer", "roles": []string{"eng-review"}})
	if err := h.store.AppendEvent(ctx, state.Event{Kind: state.EventReviewRound, Issue: n, Role: "engineer", Data: data}); err != nil {
		t.Fatal(err)
	}
	eng, _ := h.cfg.Role(config.KindWorker, "engineer")
	f, ok, err := h.loop.reviewFeedback(ctx, eng, n, branch)
	if err != nil || !ok {
		t.Fatalf("ok = %v, err = %v; an oversize role file must not fail the claim", ok, err)
	}
	if !strings.HasSuffix(string(f.Data), "\n[truncated]\n") || len(f.Data) > 70<<10 {
		t.Errorf("feedback len %d, want truncated to about 64 KiB", len(f.Data))
	}
}

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (h *harness) captureLog() *syncBuf {
	buf := &syncBuf{}
	h.log = slog.New(slog.NewTextHandler(buf, nil))
	return buf
}

// gateLicense gives license-review a go.mod path rule.
func gateLicense(h *harness) {
	for i := range h.cfg.Roles {
		if h.cfg.Roles[i].Name == "license-review" {
			h.cfg.Roles[i].When = &config.When{Paths: []string{"**/go.mod"}}
		}
	}
}

func TestReviewersDeferredBypassesWhen(t *testing.T) {
	tests := []struct {
		name     string
		deferred []string
		want     string
	}{
		{"not deferred, gated out", nil, "eng-review,sec-review"},
		{"deferred bypasses when", []string{"license-review"}, "eng-review,sec-review,license-review"},
		{"deferred twice, no duplicate", []string{"license-review", "license-review", "eng-review"}, "eng-review,sec-review,license-review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, noTests)
			h.cfg.Roles[0].Reviewers = nil
			gateLicense(h)
			h.code.shas[0] = h.commitFiles(map[string]string{"a.txt": "x"})
			n := h.issue("t")
			if len(tt.deferred) > 0 {
				h.seedDeferred(n, tt.deferred...)
			}
			h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
			h.mustTick()
			if got := strings.Join(h.only().Opts.Worker.Reviewers, ","); got != tt.want {
				t.Errorf("reviewers = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReviewersMonotonicAcrossRuns(t *testing.T) {
	h := newHarness(t, noTests)
	h.cfg.Roles[0].Reviewers = nil
	gateLicense(h)
	h.code.shas[0] = h.commitFiles(map[string]string{"a.txt": "x"})
	h.code.shas[1] = h.commitFiles(map[string]string{"a.txt": "x", "go.mod": "module x\n"})
	n := h.issue("t")
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	// The fake finisher closes nothing, so free the issue for a second claim.
	for _, j := range h.jobs() {
		if err := h.store.Complete(context.Background(), j.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.gh.RemoveLabel(context.Background(), n, labels.Running(labels.StageWorker, "engineer")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(h.fin.got()[0].Opts.Worker.Reviewers, ","); got != "eng-review,sec-review" {
		t.Fatalf("run 1 reviewers = %q, want license-review skipped", got)
	}
	h.mustTick()
	got := h.fin.got()
	if len(got) != 2 {
		t.Fatalf("FinishWith calls = %d, want 2", len(got))
	}
	// Run 2 recomputes from the whole PR diff: go.mod now matches, and the
	// reviewers run 1 requested are still selected, each once.
	if g := strings.Join(got[1].Opts.Worker.Reviewers, ","); g != "eng-review,license-review,sec-review" {
		t.Errorf("run 2 reviewers = %q, want license-review added and none duplicated", g)
	}
}

func TestReviewersStoreErrorFallsBackToLabels(t *testing.T) {
	arch := &config.When{Triage: []string{"architect"}}
	tests := []struct {
		name   string
		labels []string
		want   string
	}{
		{"label only counts", []string{labels.Done(labels.StageTriage, "architect")}, "eng-review,license-review,sec-review"},
		{"no label, gated out", nil, "eng-review,sec-review"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, noTests)
			h.cfg.Roles[0].Reviewers = nil
			h.cfg.Roles = append(h.cfg.Roles, config.Role{Kind: config.KindTriage, Name: "architect", Dir: ".ard/triage/architect"})
			for i := range h.cfg.Roles {
				if h.cfg.Roles[i].Name == "license-review" {
					h.cfg.Roles[i].When = arch
				}
			}
			buf := h.captureLog()
			h.code.shas[0] = h.commitFiles(map[string]string{"a.txt": "x"})
			n := h.issue("t", tt.labels...)
			// A triage close-out the store cannot decode makes TriageRoles fail.
			sj, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "triage", Role: "architect", Issue: n})
			if err != nil {
				t.Fatal(err)
			}
			if err := h.store.SetCloseout(context.Background(), sj.ID, "closed", []byte(`{"Version":99}`)); err != nil {
				t.Fatal(err)
			}
			if err := h.store.Complete(context.Background(), sj.ID, nil); err != nil {
				t.Fatal(err)
			}
			h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
			h.mustTick()
			if got := strings.Join(h.only().Opts.Worker.Reviewers, ","); got != tt.want {
				t.Errorf("reviewers = %q, want %q", got, tt.want)
			}
			if !strings.Contains(buf.String(), "worker.reviewers_failed") || !strings.Contains(buf.String(), "step=triage") {
				t.Errorf("no reviewers_failed step=triage in log:\n%s", buf.String())
			}
		})
	}
}

func TestReviewersEmptySelectionLogs(t *testing.T) {
	tests := []struct {
		name string
		gate bool // every reviewer path-gated on go.mod
		want int  // worker.no_reviewers lines
	}{
		{"every reviewer gated out", true, 1},
		{"reviewers selected", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, noTests)
			h.cfg.Roles[0].Reviewers = nil
			if tt.gate {
				for i := range h.cfg.Roles {
					if h.cfg.Roles[i].Kind == config.KindReviewer {
						h.cfg.Roles[i].When = &config.When{Paths: []string{"**/go.mod"}}
					}
				}
			}
			buf := h.captureLog()
			h.code.shas[0] = h.commitFiles(map[string]string{"a.txt": "x"})
			h.issue("t")
			h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
			h.mustTick()
			if tt.gate && len(h.only().Opts.Worker.Reviewers) != 0 {
				t.Errorf("reviewers = %v, want none", h.only().Opts.Worker.Reviewers)
			}
			if got := strings.Count(buf.String(), "worker.no_reviewers"); got != tt.want {
				t.Errorf("worker.no_reviewers lines = %d, want %d\n%s", got, tt.want, buf.String())
			}
		})
	}
}

type readingSink struct{ n int }

func (r *readingSink) Observe(_ context.Context, ws []agent.Window) { r.n += len(ws) }

// Readings are recorded for the agent step and each fix step, and never for a
// test step.
func TestReadingsRecordedOnlyForAgentSteps(t *testing.T) {
	h := newHarness(t)
	h.issue("Crash")
	sink := &readingSink{}
	h.loop.d.Readings = sink
	h.loop.d.Parse = func(_ context.Context, _ *config.Resolved, _ state.Job, hd executor.Handle, _ int) (agent.Result, error) {
		return agent.Result{Windows: []agent.Window{{Type: "five_hour", Utilization: 0.5}}}, nil
	}
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, func(int) int { return 1 })
	h.mustTick()
	if sink.n != 3 { // the agent step and two fix steps, as in TestStepUsageRecordedForEveryAgentStep
		t.Errorf("readings recorded %d times, want 3 (no test step)", sink.n)
	}
}

// readingFin forwards a step's windows to its sink as reaper.Finisher does.
type readingFin struct {
	fakeFin
	sink *readingSink
}

func (f *readingFin) FinishWith(ctx context.Context, id string, h executor.Handle, st executor.Status, res agent.Result, o reaper.FinishOptions) (closeout.Outcome, error) {
	if len(res.Windows) > 0 {
		f.sink.Observe(ctx, res.Windows)
	}
	return f.fakeFin.FinishWith(ctx, id, h, st, res, o)
}

// The final agent step's reading is recorded once: by agentStep, not again
// at finish, where it could overwrite a newer reading from another job.
func TestReadingRecordedOncePerStep(t *testing.T) {
	h := newHarness(t)
	h.issue("Crash")
	sink := &readingSink{}
	h.loop.d.Readings = sink
	h.loop.d.Finisher = &readingFin{sink: sink}
	h.loop.d.Parse = func(_ context.Context, _ *config.Resolved, _ state.Job, hd executor.Handle, _ int) (agent.Result, error) {
		return agent.Result{Windows: []agent.Window{{Type: "five_hour", Utilization: 0.5}}}, nil
	}
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, func(int) int { return 0 })
	h.mustTick()
	if sink.n != 1 {
		t.Errorf("reading recorded %d times, want 1", sink.n)
	}
}

func TestNetworkPerStep(t *testing.T) {
	for _, tc := range []struct {
		name          string
		network       bool
		test, notTest executor.Network
	}{
		{"false", false, executor.NetworkNone, executor.NetworkAdvisory},
		{"true", true, executor.NetworkOn, executor.NetworkOn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.cfg.Roles[0].Network = tc.network
			n := h.issue("Crash")
			tk := 0
			h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
				if kind == "test" {
					tk++
					if tk == 1 {
						return 1
					}
					return 0
				}
				writeFile(t, hd.Worktree, "hello.txt", "x"+kind)
				writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", n), roleDone)
				return 0
			}
			h.mustTick()
			var tests, others int
			for _, j := range h.ex.jobs {
				want := tc.notTest
				if kindOf(j.ID) == "test" {
					want = tc.test
					tests++
				} else {
					others++
				}
				if j.Network != want {
					t.Errorf("job %s (%s): Network = %q, want %q", j.ID, kindOf(j.ID), j.Network, want)
				}
			}
			if tests != 2 || others < 2 {
				t.Errorf("tests=%d others=%d", tests, others)
			}
		})
	}
}
