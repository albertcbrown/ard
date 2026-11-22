package servecmd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
	"github.com/albertcbrown/ard/internal/worker"
)

// A fake claude for the worker: it writes hello.txt ("bye", then "hi" on a fix
// round, which it recognises by .ard-input/<n>/test-output.log) and its role
// file, and leaks the token on stderr.
const workerAgent = `#!/bin/sh
for d in .ard-input/*; do n=${d##*/}; done
test -n "$CLAUDE_CODE_OAUTH_TOKEN" || exit 4
echo "using token $CLAUDE_CODE_OAUTH_TOKEN" >&2
if [ -f ".ard-input/$n/test-output.log" ]; then echo hi > hello.txt; else echo bye > hello.txt; fi
mkdir -p ".ard/$n"
cat > ".ard/$n/worker-engineer.md" <<'EOF2'
---
role: engineer
status: done
attempt: 1
---
Fixes #99 by adding hello
EOF2
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"num_turns":1,"total_cost_usd":0.01,"session_id":"s","result":"ok"}'
`

// guardAgent edits .github/CODEOWNERS, which the diff guard refuses.
var guardAgent = strings.Replace(workerAgent, "mkdir -p \".ard/$n\"", "mkdir -p .github \".ard/$n\"\necho '* @someone' > .github/CODEOWNERS", 1)

type workerE2E struct {
	t      *testing.T
	w      *wiring
	gh     *fake.Fake
	bare   string
	cfg    *config.Resolved
	loop   *worker.Loop
	issueN int
	main0  string
}

func newWorkerE2E(t *testing.T, script string) *workerE2E {
	t.Helper()
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", e2eToken)
	w := newTestWiring(t)
	w.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	stage, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w.StateDir = stage
	bare, clone := gittest.NewRemote(t)
	gh := fake.New(fake.WithIdentity(github.Identity{Login: "ard[bot]", ID: 4242, Kind: github.AuthApp}))
	w.Reader, w.Effects = gh, effects.NewLive(gh, gitx.New(""), w.Log)
	w.Root, w.RemoteURL = clone, "file://"+bare
	w.Model, err = modelauth.Resolve("", claude.New(claude.Options{}).CredentialEnv, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	e := &workerE2E{t: t, w: w, gh: gh, bare: bare}
	e.issueN = gh.SeedIssue(github.Issue{Title: "Say hello", Body: "Add hello.txt.", Author: "ard[bot]",
		Labels: []string{labels.Request(labels.StageWorker, "engineer"), labels.Approved, "type:feature"}, CreatedAt: time.Now().Add(-time.Hour)})

	// main holds check.sh and the plan triage committed.
	if err := os.WriteFile(filepath.Join(clone, "check.sh"), []byte("test \"$(cat hello.txt)\" = hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(clone, ".ard", itoa(e.issueN)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(clone, ".ard", itoa(e.issueN), "plan.md"), []byte("## Implementation\nAdd hello.txt.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "plan")
	gittest.Run(t, clone, "push", "origin", "HEAD:main")
	sha := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	e.main0 = sha

	rolesRoot := t.TempDir()
	role := func(kind config.Kind, name, dir string) config.Role {
		if err := os.MkdirAll(filepath.Join(rolesRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rolesRoot, dir, "prompt.txt"), []byte("prompt\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return config.Role{Kind: kind, Name: name, Dir: dir, Agent: "claude", Timeout: 10 * time.Minute}
	}
	eng := role(config.KindWorker, "engineer", ".ard/workers/engineer")
	eng.Tests, eng.MaxTestFixes, eng.Reviewers = "sh check.sh", 2, []string{"eng-review"}
	e.cfg = &config.Resolved{Repo: rolesRoot, SHA: sha, MaxBudgetUSD: 5, MaxPods: 3, RequireApproval: true, Intake: config.Intake{Mode: config.IntakeLabel},
		Roles: []config.Role{role(config.KindTriage, "lead", ".ard/triage/lead"), eng,
			role(config.KindReviewer, "eng-review", ".ard/reviewers/eng-review"),
			role(config.KindReviewer, "license-review", ".ard/reviewers/license-review")}}
	w.cfg.Store(e.cfg)
	w.pool.Store(pool.New(pool.Options{Max: 3}))

	// A closed lead close-out that deferred a license review.
	ctx := context.Background()
	sj, err := w.Store.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "lead", Issue: e.issueN})
	if err != nil {
		t.Fatal(err)
	}
	rec, _ := json.Marshal(closeout.Record{Version: 1, Phase: closeout.PhaseClosed, Job: closeout.Job{ID: sj.ID, Role: "lead", Stage: "triage"},
		Plan: closeout.Plan{Outcome: closeout.OutcomeDone, Deferred: []closeout.NextReq{{Stage: "review", Role: "license-review"}}}})
	if err := w.Store.SetCloseout(ctx, sj.ID, "closed", rec); err != nil {
		t.Fatal(err)
	}
	if err := w.Store.Complete(ctx, sj.ID, nil); err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	reg, err := agent.NewRegistry(claude.New(claude.Options{Binary: bin}))
	if err != nil {
		t.Fatal(err)
	}
	ex, err := w.newExecutor()
	if err != nil {
		t.Fatal(err)
	}
	fin, err := w.finisher()
	if err != nil {
		t.Fatal(err)
	}
	e.loop = worker.New(worker.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store, Executor: ex, Finisher: fin,
		Code: w.branchCommitter(), Git: gitx.New(""),
		Pool:   func() worker.Offerer { return w.Pool() },
		Agents: reg, Parse: w.parseFunc(), Model: w.Model,
		Repo: w.Repo, RepoDir: w.Root, RemoteURL: w.RemoteURL, Poll: 20 * time.Millisecond, Log: w.Log,
	})
	return e
}

func (e *workerE2E) tick() {
	e.t.Helper()
	ctx := context.Background()
	in := controller.NewInflight(ctx, e.w.Log)
	if err := e.loop.Tick(ctx, controller.Env{Cfg: e.cfg, Log: e.w.Log, Inflight: in}); err != nil {
		e.t.Fatalf("tick: %v", err)
	}
	select {
	case <-in.Wait():
	case <-time.After(120 * time.Second):
		e.t.Fatal("watchers did not finish")
	}
}

func (e *workerE2E) issue() github.Issue {
	is, err := e.gh.GetIssue(context.Background(), e.issueN)
	if err != nil {
		e.t.Fatal(err)
	}
	return is
}

func (e *workerE2E) hasLabel(ls []string, want string) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}

// The worker loop must use the one executor and Finisher the Reaper uses, and
// the shared per-branch committer for the code lane.
func TestWorkerLoopSharesParts(t *testing.T) {
	w := newTestWiring(t)
	l := workerLoop(w)
	if l == nil {
		t.Fatal("no worker loop")
	}
	wl, ok := l.(*worker.Loop)
	if !ok {
		t.Fatalf("loop is %T", l)
	}
	ex, _ := w.newExecutor()
	fin, _ := w.finisher()
	if got := field(t, wl, "d", "Executor"); got != any(ex) {
		t.Errorf("executor %v is not the shared one", got)
	}
	if got := field(t, wl, "d", "Finisher"); got != any(fin) {
		t.Errorf("finisher %v is not the shared one", got)
	}
	if got := field(t, wl, "d", "Code"); got != any(w.branchCommitter()) {
		t.Errorf("code committer %v is not the shared branch committer", got)
	}
}

func TestWorkerEndToEndDraftPR(t *testing.T) {
	e := newWorkerE2E(t, workerAgent)
	e.tick()
	n := e.issueN

	is := e.issue()
	if !e.hasLabel(is.Labels, labels.Done(labels.StageWorker, "engineer")) || e.hasLabel(is.Labels, labels.Running(labels.StageWorker, "engineer")) ||
		e.hasLabel(is.Labels, labels.TestsFailed) || e.hasLabel(is.Labels, labels.NeedsHuman) || e.hasLabel(is.Labels, labels.ExecutorFailed) {
		t.Fatalf("labels = %v", is.Labels)
	}
	prs, err := e.gh.ListPRs(context.Background(), github.ListPRsOpts{State: github.StateOpen})
	if err != nil || len(prs) != 1 {
		t.Fatalf("prs = %v, %v", prs, err)
	}
	pr := prs[0]
	branch := "worker/engineer-" + itoa(n) + "-say-hello"
	if !pr.Draft || pr.HeadRef != branch || pr.BaseRef != "main" {
		t.Errorf("pr = %+v", pr)
	}
	if strings.Count(pr.Body, "Closes #") != 1 || !strings.Contains(pr.Body, "Closes #"+itoa(n)) || !strings.Contains(pr.Body, "Fixes issue 99") || strings.Contains(pr.Body, "Fixes #99") {
		t.Errorf("body:\n%s", pr.Body)
	}
	for _, want := range []string{"ard:review:eng-review", "ard:review:license-review"} {
		if !e.hasLabel(pr.Labels, want) {
			t.Errorf("PR labels %v lack %s", pr.Labels, want)
		}
	}
	if e.hasLabel(pr.Labels, labels.TestsFailed) {
		t.Errorf("PR labels = %v", pr.Labels)
	}

	// Two code commits, then close-out's commit with the role file and both logs.
	subjects := strings.Split(strings.TrimSpace(gittest.Run(t, e.bare, "log", "--format=%s", "main.."+branch)), "\n")
	code := "feat: Say hello (#" + itoa(n) + ")"
	if len(subjects) != 3 || subjects[1] != code || subjects[2] != code || !strings.HasPrefix(subjects[0], "ard(worker:engineer)") {
		t.Fatalf("subjects = %q", subjects)
	}
	if got := strings.TrimSpace(gittest.Run(t, e.bare, "show", branch+":hello.txt")); got != "hi" {
		t.Errorf("hello.txt = %q", got)
	}
	var logs int
	for _, f := range strings.Fields(gittest.Run(t, e.bare, "ls-tree", "-r", "--name-only", branch)) {
		switch {
		case strings.Contains(f, ".ard-input"):
			t.Errorf("untrusted input committed: %s", f)
		case strings.HasPrefix(f, ".ard/"+itoa(n)+"/logs/"):
			logs++
			if b := gittest.Run(t, e.bare, "show", branch+":"+f); strings.Contains(b, e2eToken) || !strings.Contains(b, "using token") {
				t.Errorf("committed log %s must hold the redacted line, not the token", f)
			}
		}
	}
	if logs != 2 {
		t.Errorf("logs committed = %d, want the two agent logs", logs)
	}
	if got := gittest.Run(t, e.bare, "show", branch+":.ard/"+itoa(n)+"/worker-engineer.md"); !strings.Contains(got, "status: done") {
		t.Errorf("role file = %s", got)
	}
	if got := strings.TrimSpace(gittest.Run(t, e.bare, "rev-parse", "main")); got != e.main0 {
		t.Errorf("main moved: %s", got)
	}

	cs, _ := e.gh.ListComments(context.Background(), n, time.Time{})
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "Tests: passed after 1 fix attempt") || !strings.Contains(cs[0].Body, "PR: ") {
		t.Errorf("comments = %+v", cs)
	}
	var j state.Job
	evs, _ := e.w.Store.Events(context.Background(), state.EventFilter{Kind: state.EventClaim})
	var claims []state.Event
	for _, ev := range evs {
		if ev.Stage == "worker" {
			claims = append(claims, ev)
		}
	}
	if len(claims) != 1 {
		t.Fatalf("worker claims = %d", len(claims))
	}
	if j, err = e.w.Store.Job(context.Background(), claims[0].JobID); err != nil || j.Status != state.StatusDone || j.Branch != branch || j.PR != pr.Number {
		t.Errorf("job = %+v, %v", j, err)
	}
}

func TestWorkerEndToEndGuardHold(t *testing.T) {
	e := newWorkerE2E(t, guardAgent)
	e.tick()
	n := e.issueN
	is := e.issue()
	if !e.hasLabel(is.Labels, labels.NeedsHuman) || e.hasLabel(is.Labels, labels.Done(labels.StageWorker, "engineer")) {
		t.Fatalf("labels = %v", is.Labels)
	}
	if prs, _ := e.gh.ListPRs(context.Background(), github.ListPRsOpts{State: github.StateOpen}); len(prs) != 0 {
		t.Errorf("a PR was opened: %v", prs)
	}
	if refs := gittest.Run(t, e.bare, "for-each-ref", "refs/heads/worker"); strings.TrimSpace(refs) != "" {
		t.Errorf("a branch was created:\n%s", refs)
	}
	files := strings.Fields(gittest.Run(t, e.bare, "ls-tree", "-r", "--name-only", "main"))
	var role, log bool
	for _, f := range files {
		switch {
		case f == ".ard/"+itoa(n)+"/worker-engineer.md":
			role = true
		case strings.HasPrefix(f, ".ard/"+itoa(n)+"/logs/"):
			log = true
		case f == ".github/CODEOWNERS" || f == "hello.txt":
			t.Errorf("code reached main: %s", f)
		}
	}
	if !role || !log {
		t.Errorf("main files = %v", files)
	}
	cs, _ := e.gh.ListComments(context.Background(), n, time.Time{})
	if len(cs) != 1 || !strings.Contains(cs[0].Body, ".github/CODEOWNERS") || !strings.Contains(cs[0].Body, "diff guard") {
		t.Errorf("comments = %+v", cs)
	}
}
