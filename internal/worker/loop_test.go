package worker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	tok := "AK" + "IA" + "IOSFODNN7EXAMPLE"
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
			return old + "var k3 = \"AK\" + \"IA\" + \"IOSFODNN7EXAMPLE\"\n"
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
	if fc.Opts.Cause.Detail != "could not open the pull request" || fc.Opts.Cause.Retry {
		t.Errorf("cause = %+v", fc.Opts.Cause)
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
	if len(h.prs()) != 1 || fc.Opts.Worker == nil || fc.Opts.Worker.PR == 0 {
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

func TestMoreThanOneThousandLinesCannotBeHiddenFromTheCount(t *testing.T) {
	h := newHarness(t, noTests)
	h.issue("t")
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		writeFile(t, hd.Worktree, "big.txt", "x\n")
		gittest.Run(t, hd.Worktree, "add", "big.txt")
		gittest.Run(t, hd.Worktree, "update-index", "--skip-worktree", "big.txt")
		writeFile(t, hd.Worktree, "big.txt", strings.Repeat("x\n", 1500))
		writeFile(t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", j.Issue), roleDone)
		return 0
	}
	h.mustTick()
	if w := h.only().Opts.Worker; w == nil || !strings.Contains(w.Hold, "changed lines") {
		t.Errorf("worker = %+v", w)
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
