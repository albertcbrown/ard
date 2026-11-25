package servecmd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
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
	"github.com/albertcbrown/ard/internal/reviewer"
	"github.com/albertcbrown/ard/internal/triage"
	"github.com/albertcbrown/ard/internal/worker"
)

// networkAgent is one fake claude for every role of the network scenario. It
// reads its stage and role from the user message and keeps a run counter per
// role in @STATE@. No agent touches the network.
//
//   - triage lead: routes to the architect and the engineer, every run.
//   - triage architect: done.
//   - triage engineer: plan.md on the first run; when network-request.md is
//     there, a "### Network inputs" section and an inputs: list as well. The
//     provider of the input is in @STATE@/provider (human by default).
//   - worker: needs-network on its first run (on every run when @STATE@/always
//     exists); after that it needs testdata/spec.json from main.
//   - reviewer: approves.
const networkAgent = `#!/bin/sh
S=@STATE@
for d in .ard-input/*; do n=${d##*/}; done
test -n "$CLAUDE_CODE_OAUTH_TOKEN" || exit 4
msg="$*"
stage=$(printf '%s\n' "$msg" | sed -n 's/.*You are the ard \([a-z]*\) role .*/\1/p' | head -1)
role=$(printf '%s\n' "$msg" | sed -n 's/.*You are the ard [a-z]* role "\([a-z-]*\)".*/\1/p' | head -1)
c=$(cat "$S/$stage-$role" 2>/dev/null || echo 0); c=$((c+1)); echo $c > "$S/$stage-$role"
mkdir -p ".ard/$n"
case "$stage-$role" in
triage-lead)
cat > ".ard/$n/triage-lead.md" <<'EOF2'
---
role: lead
status: done
attempt: 1
next: [triage:architect, triage:engineer]
worker: engineer
---
Routed to the architect and the engineer.
EOF2
;;
triage-architect)
cat > ".ard/$n/triage-architect.md" <<'EOF2'
---
role: architect
status: done
attempt: 1
---
Design noted.
EOF2
;;
triage-engineer)
if [ -f ".ard-input/$n/network-request.md" ]; then
  grep -q 'spec.json' ".ard-input/$n/network-request.md" || exit 7
  printf '\n### Network inputs\n\n- testdata/spec.json from the vendor, pinned.\n' >> ".ard/$n/plan.md"
  p=$(cat "$S/provider" 2>/dev/null || echo human)
  cat > ".ard/$n/triage-engineer.md" <<EOF2
---
role: engineer
status: done
attempt: 1
inputs:
  - path: testdata/spec.json
    provider: $p
    url: https://example.com/spec.json
---
Network inputs declared.
EOF2
else
  printf '\n## Implementation\n\n1. Add hello.txt from testdata/spec.json.\n' >> ".ard/$n/plan.md"
  cat > ".ard/$n/triage-engineer.md" <<'EOF2'
---
role: engineer
status: done
attempt: 1
---
Implementation steps written.
EOF2
fi
;;
worker-engineer)
if [ "$c" = 1 ] || [ -f "$S/always" ]; then
  cat > ".ard/$n/worker-engineer.md" <<'EOF2'
---
role: engineer
status: needs-network
attempt: 1
network:
  - what: the vendor spec
    url: https://example.com/spec.json
    why: the plan needs the schema
---
The spec cannot be fetched here.
EOF2
else
  test -f testdata/spec.json || exit 8
  echo hi > hello.txt
  cat > ".ard/$n/worker-engineer.md" <<'EOF2'
---
role: engineer
status: done
attempt: 1
---
Added hello from the spec.
EOF2
fi
;;
review-eng-review)
cat > ".ard/$n/review-eng-review.md" <<'EOF2'
---
role: eng-review
status: done
attempt: 1
verdict: approve
---
Looks good.
EOF2
;;
*) exit 9 ;;
esac
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"num_turns":1,"total_cost_usd":0.01,"session_id":"s","result":"ok"}'
`

// networkE2E runs the triage, worker and reviewer loops against one fake
// GitHub, one remote and one store, driven by explicit ticks.
type networkE2E struct {
	t        *testing.T
	w        *wiring
	gh       *fake.Fake
	bare     string
	cfg      *config.Resolved
	triage   *triage.Loop
	worker   *worker.Loop
	reviewer *reviewer.Loop
	state    string
	issueN   int
}

func newNetworkE2E(t *testing.T) *networkE2E {
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
	e := &networkE2E{t: t, w: w, gh: gh, bare: bare, state: t.TempDir()}

	if err := os.WriteFile(filepath.Join(clone, "README.md"), []byte("# readme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "readme")
	gittest.Run(t, clone, "push", "origin", "HEAD:main")

	rolesRoot := t.TempDir()
	role := func(kind config.Kind, name, dir string) config.Role {
		if err := os.MkdirAll(filepath.Join(rolesRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rolesRoot, dir, "prompt.txt"), []byte("prompt\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return config.Role{Kind: kind, Name: name, Dir: dir, Agent: "claude", Timeout: 10 * time.Minute, Network: true}
	}
	eng := role(config.KindTriage, "engineer", ".ard/triage/engineer")
	eng.PlanSection = config.BuiltinPlanSections["engineer"]
	wk := role(config.KindWorker, "engineer", ".ard/workers/engineer")
	wk.Network = false
	wk.Tests, wk.MaxTestFixes, wk.Reviewers = "test -f hello.txt", 2, []string{"eng-review"}
	wk.DiffWarnLines, wk.DiffMaxLines = 1000, 2000
	e.cfg = &config.Resolved{Repo: rolesRoot, MaxBudgetUSD: 5, MaxPods: 3, RequireApproval: true, Intake: config.Intake{Mode: config.IntakeLabel},
		Roles: []config.Role{role(config.KindTriage, "lead", ".ard/triage/lead"), role(config.KindTriage, "architect", ".ard/triage/architect"),
			eng, wk, role(config.KindReviewer, "eng-review", ".ard/reviewers/eng-review")}}
	e.syncMain()
	w.cfg.Store(e.cfg)
	w.pool.Store(pool.New(pool.Options{Max: 3}))

	e.issueN = gh.SeedIssue(github.Issue{Title: "Say hello", Body: "Add hello.txt.", Author: "alice", Labels: []string{labels.Pending}, CreatedAt: time.Now().Add(-time.Hour)})
	gh.SeedTimeline(e.issueN, github.TimelineEvent{Event: "labeled", Label: labels.Pending, Actor: "maint", CreatedAt: time.Now().Add(-30 * time.Minute)})
	gh.SetPermission("maint", github.Permission{Role: "maintain"})

	bin := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(bin, []byte(strings.Replace(networkAgent, "@STATE@", e.state, 1)), 0o755); err != nil {
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
	e.triage = triage.New(triage.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store, Executor: ex, Finisher: fin,
		Pool:   func() triage.Offerer { return w.Pool() },
		Agents: reg, Parse: w.parseFunc(), Model: w.Model,
		Repo: w.Repo.String(), RepoDir: w.Root, Poll: 20 * time.Millisecond, Log: w.Log,
		PlanExists: planExists(w), MainSHA: mainSHA(w), MainEntry: mainEntry(w),
	})
	e.worker = worker.New(worker.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store, Executor: ex, Finisher: fin,
		Code: w.branchCommitter(), Git: gitx.New(""),
		Pool:   func() worker.Offerer { return w.Pool() },
		Agents: reg, Parse: w.parseFunc(), Model: w.Model,
		Repo: w.Repo, RepoDir: w.Root, RemoteURL: w.RemoteURL, Poll: 20 * time.Millisecond, Log: w.Log,
	})
	e.reviewer = reviewer.New(reviewer.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store, Executor: ex, Finisher: fin, Git: gitx.New(""),
		Pool:   func() reviewer.Offerer { return w.Pool() },
		Agents: reg, Parse: w.parseFunc(), Model: w.Model,
		Repo: w.Repo, RepoDir: w.Root, RemoteURL: w.RemoteURL, Poll: 20 * time.Millisecond, Log: w.Log,
	})
	return e
}

// syncMain points the config at the tip of main on the remote, so a job's
// worktree starts from what close-out committed.
func (e *networkE2E) syncMain() {
	e.t.Helper()
	gittest.Run(e.t, e.w.Root, "fetch", "origin")
	e.cfg.SHA = strings.TrimSpace(gittest.Run(e.t, e.w.Root, "rev-parse", "origin/main"))
}

func (e *networkE2E) setState(name, body string) {
	e.t.Helper()
	if err := os.WriteFile(filepath.Join(e.state, name), []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

func (e *networkE2E) runs(stage, role string) string {
	b, _ := os.ReadFile(filepath.Join(e.state, stage+"-"+role))
	return strings.TrimSpace(string(b))
}

func (e *networkE2E) tick(l controller.Loop) {
	e.t.Helper()
	ctx := context.Background()
	in := controller.NewInflight(ctx, e.w.Log)
	if err := l.Tick(ctx, controller.Env{Cfg: e.cfg, Log: e.w.Log, Inflight: in}); err != nil {
		e.t.Fatalf("tick: %v", err)
	}
	select {
	case <-in.Wait():
	case <-time.After(120 * time.Second):
		e.t.Fatal("watchers did not finish")
	}
	e.syncMain()
}

func (e *networkE2E) triageTicks(n int) {
	e.t.Helper()
	for i := 0; i < n; i++ {
		e.tick(e.triage)
	}
}

func (e *networkE2E) labels() []string {
	is, err := e.gh.GetIssue(context.Background(), e.issueN)
	if err != nil {
		e.t.Fatal(err)
	}
	return is.Labels
}

func (e *networkE2E) has(label string) bool { return has(e.labels(), label) }

func (e *networkE2E) count(substr string) int {
	cs, err := e.gh.ListComments(context.Background(), e.issueN, time.Time{})
	if err != nil {
		e.t.Fatal(err)
	}
	n := 0
	for _, c := range cs {
		if strings.Contains(c.Body, substr) {
			n++
		}
	}
	return n
}

func (e *networkE2E) approve() {
	e.t.Helper()
	time.Sleep(1100 * time.Millisecond) // the approval must come after the last :done, at second resolution
	if e.has(labels.Approved) {         // an approval given before the new round does not count: remove and re-add
		if err := e.gh.As("maint").RemoveLabel(context.Background(), e.issueN, labels.Approved); err != nil {
			e.t.Fatal(err)
		}
		time.Sleep(1100 * time.Millisecond)
	}
	if err := e.gh.As("maint").AddLabels(context.Background(), e.issueN, labels.Approved); err != nil {
		e.t.Fatal(err)
	}
}

func (e *networkE2E) landInput() {
	e.t.Helper()
	clone := e.w.Root
	gittest.Run(e.t, clone, "checkout", "-B", "land", "origin/main")
	p := filepath.Join(clone, "testdata", "spec.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("{}\n"), 0o644); err != nil {
		e.t.Fatal(err)
	}
	gittest.Run(e.t, clone, "add", "-A")
	gittest.Run(e.t, clone, "commit", "-m", "land the vendor spec")
	gittest.Run(e.t, clone, "push", "origin", "HEAD:main")
	gittest.Run(e.t, clone, "checkout", "main")
	e.syncMain()
}

// toWorker drives triage until the first approval starts the worker.
func (e *networkE2E) toWorker() {
	e.t.Helper()
	for i := 0; i < 6 && e.count("ready for approval") == 0; i++ {
		e.tick(e.triage) // the lead, then the architect and the engineer, then the approval request
	}
	if e.count("ready for approval") != 1 || e.has(labels.Request(labels.StageWorker, "engineer")) {
		e.t.Fatalf("triage did not stop for approval: %v", e.labels())
	}
	e.approve()
	e.tick(e.triage)
	if !e.has(labels.Request(labels.StageWorker, "engineer")) {
		e.t.Fatalf("worker not requested: %v", e.labels())
	}
}

func (e *networkE2E) requireNeedsHuman(want string) {
	e.t.Helper()
	if !e.has(labels.NeedsHuman) {
		e.t.Fatalf("not needs-human: %v", e.labels())
	}
	if e.has(labels.Request(labels.StageTriage, "engineer")) && !e.has(labels.Done(labels.StageTriage, "engineer")) {
		e.t.Errorf("the engineer was requested: %v", e.labels())
	}
	if want != "" && e.count(want) == 0 {
		e.t.Errorf("no comment with %q", want)
	}
	if e.count("spec.json") == 0 {
		e.t.Errorf("the needs-human comment does not list the network entries")
	}
}

func TestNetworkBounceEndToEnd(t *testing.T) {
	e := newNetworkE2E(t)
	n := itoa(e.issueN)
	wlabel := labels.Request(labels.StageWorker, "engineer")
	e.toWorker()

	// The worker answers needs-network: it is bounced to the triage engineer.
	e.tick(e.worker)
	for _, gone := range []string{wlabel, labels.Running(labels.StageWorker, "engineer"), labels.Done(labels.StageTriage, "engineer")} {
		if e.has(gone) {
			t.Errorf("label %s still set after the bounce: %v", gone, e.labels())
		}
	}
	if !e.has(labels.Request(labels.StageTriage, "engineer")) || e.has(labels.NeedsHuman) {
		t.Fatalf("the engineer was not requested: %v", e.labels())
	}
	if out := strings.TrimSpace(gittest.Run(t, e.bare, "branch", "--list", "worker/*")); out != "" {
		t.Errorf("a branch exists after the bounce: %s", out)
	}
	if prs, _ := e.gh.ListPRs(context.Background(), github.ListPRsOpts{State: github.StateOpen}); len(prs) != 0 {
		t.Errorf("a pull request exists after the bounce: %+v", prs)
	}

	// The engineer declares the input; the lead re-runs; then the other roles.
	e.tick(e.triage)
	if e.runs("triage", "engineer") != "2" {
		t.Fatalf("engineer runs = %s", e.runs("triage", "engineer"))
	}
	if e.has(labels.Done(labels.StageTriage, "lead")) {
		t.Errorf("the lead's done was not cleared: %v", e.labels())
	}
	for i := 0; i < 6 && (e.runs("triage", "lead") != "2" || e.runs("triage", "architect") != "2"); i++ {
		e.tick(e.triage)
	}
	if e.runs("triage", "lead") != "2" || e.runs("triage", "architect") != "2" {
		t.Fatalf("re-run: lead %s, architect %s; labels %v", e.runs("triage", "lead"), e.runs("triage", "architect"), e.labels())
	}

	// The input is not on main: the gate holds, with one comment.
	e.triageTicks(3)
	if e.count("waiting for inputs") != 1 || e.count("testdata/spec.json") == 0 {
		t.Fatalf("waiting comments = %d", e.count("waiting for inputs"))
	}
	if e.has(wlabel) || e.count("ready for approval") != 1 {
		t.Fatalf("the gate did not hold: %v", e.labels())
	}
	if got := e.runs("worker", "engineer"); got != "1" {
		t.Fatalf("worker runs = %s", got)
	}

	// The input lands on main: a fresh "ready for approval" for the new round.
	e.landInput()
	e.triageTicks(2)
	if e.count("ready for approval") != 2 {
		t.Fatalf("approval comments = %d; labels %v", e.count("ready for approval"), e.labels())
	}
	if e.has(wlabel) {
		t.Fatalf("the worker started on the stale approval: %v", e.labels())
	}
	e.approve()
	e.tick(e.triage)
	if !e.has(wlabel) {
		t.Fatalf("worker not requested after the new approval: %v", e.labels())
	}

	// The worker runs again, pushes the branch and opens a draft pull request.
	e.tick(e.worker)
	prs, err := e.gh.ListPRs(context.Background(), github.ListPRsOpts{State: github.StateOpen})
	if err != nil || len(prs) != 1 || !prs[0].Draft {
		t.Fatalf("prs = %+v, err %v; labels %v", prs, err, e.labels())
	}
	branch := worker.BranchName("engineer", e.issueN, "Say hello")
	if prs[0].HeadRef != branch {
		t.Errorf("head = %s, want %s", prs[0].HeadRef, branch)
	}
	if got := gittest.Run(t, e.bare, "show", branch+":hello.txt"); strings.TrimSpace(got) != "hi" {
		t.Errorf("hello.txt = %q", got)
	}
	if !has(prs[0].Labels, labels.Request(labels.StageReview, "eng-review")) {
		t.Fatalf("PR labels = %v", prs[0].Labels)
	}

	// The reviewer reviews it. The fake GitHub does not track git, so tell it
	// where the branch is.
	e.gh.SetHeadSHA(prs[0].Number, strings.TrimSpace(gittest.Run(t, e.bare, "rev-parse", branch)))
	e.tick(e.reviewer)
	var reviews int
	for _, c := range e.gh.Writes() {
		if c.Method == "CreateReview" {
			reviews++
		}
	}
	if reviews != 1 {
		t.Errorf("reviews = %d", reviews)
	}
	if got := gittest.Run(t, e.bare, "ls-tree", "-r", "--name-only", branch); !strings.Contains(got, ".ard/"+n+"/review-eng-review.md") {
		t.Errorf("the review role file is not on the branch:\n%s", got)
	}
}

func TestNetworkSecondRoundNeedsHuman(t *testing.T) {
	e := newNetworkE2E(t)
	e.setState("always", "1")
	e.toWorker()
	e.tick(e.worker) // round one: bounced
	if !e.has(labels.Request(labels.StageTriage, "engineer")) {
		t.Fatalf("not bounced: %v", e.labels())
	}
	e.landInput()
	for i := 0; i < 8 && !e.has(labels.Request(labels.StageWorker, "engineer")) && e.count("ready for approval") < 2; i++ {
		e.tick(e.triage)
	}
	e.approve()
	e.tick(e.triage)
	if !e.has(labels.Request(labels.StageWorker, "engineer")) {
		t.Fatalf("worker not requested: %v", e.labels())
	}
	e.tick(e.worker) // round two: no second bounce
	e.requireNeedsHuman("")
	if e.runs("triage", "engineer") != "2" {
		t.Errorf("engineer runs = %s", e.runs("triage", "engineer"))
	}
}

func TestNetworkBranchExistsNeedsHuman(t *testing.T) {
	e := newNetworkE2E(t)
	e.toWorker()
	branch := worker.BranchName("engineer", e.issueN, "Say hello")
	gittest.Run(t, e.w.Root, "push", "origin", "HEAD:refs/heads/"+branch)
	e.tick(e.worker)
	e.requireNeedsHuman("")
	if e.runs("triage", "engineer") != "1" {
		t.Errorf("engineer runs = %s", e.runs("triage", "engineer"))
	}
}

func TestNetworkWorkerInputWithNetworkFalse(t *testing.T) {
	e := newNetworkE2E(t)
	e.setState("provider", "worker")
	e.toWorker()
	e.tick(e.worker) // bounced
	for i := 0; i < 10 && !e.has(labels.NeedsHuman); i++ {
		e.tick(e.triage)
	}
	if !e.has(labels.NeedsHuman) {
		t.Fatalf("the gate did not stop: %v", e.labels())
	}
	if e.has(labels.Request(labels.StageWorker, "engineer")) {
		t.Errorf("a worker was requested: %v", e.labels())
	}
	if got := e.runs("worker", "engineer"); got != "1" {
		t.Errorf("worker runs = %s", got)
	}
}
