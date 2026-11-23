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
)

// A fake claude for the reviewer. It checks that the diff reached it, writes
// its role file (a blocking finding on an added line, one on a file the PR did
// not touch) and leaks the token on stderr. It also tries to edit code, which
// close-out must never collect.
const reviewerAgent = `#!/bin/sh
for d in .ard-input/*; do n=${d##*/}; done
test -n "$CLAUDE_CODE_OAUTH_TOKEN" || exit 4
grep -q 'hello.go' ".ard-input/$n/diff.patch" || exit 5
grep -q 'It is broken' ".ard-input/$n/issue.md" || exit 6
echo "using token $CLAUDE_CODE_OAUTH_TOKEN" >&2
echo tampered > hello.go
mkdir -p ".ard/$n"
cat > ".ard/$n/review-eng-review.md" <<'EOF2'
---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: hello.go
    line: 2
    severity: high
    blocking: true
    message: The function ignores its argument.
  - file: README.md
    line: 1
    severity: low
    message: The readme is stale.
---
One blocking problem.
EOF2
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"num_turns":1,"total_cost_usd":0.01,"session_id":"s","result":"ok"}'
`

type reviewE2E struct {
	t      *testing.T
	w      *wiring
	gh     *fake.Fake
	bare   string
	cfg    *config.Resolved
	loop   *reviewer.Loop
	issueN int
	prN    int
	branch string
	tip    string
	main0  string
}

func newReviewE2E(t *testing.T, script string) *reviewE2E {
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
	e := &reviewE2E{t: t, w: w, gh: gh, bare: bare}
	e.issueN = gh.SeedIssue(github.Issue{Title: "Say hello", Body: "It is broken.", Author: "ard[bot]", CreatedAt: time.Now().Add(-time.Hour)})
	n := itoa(e.issueN)
	write := func(rel, body string) {
		p := filepath.Join(clone, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md", "# readme\n")
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "readme")
	gittest.Run(t, clone, "push", "origin", "HEAD:main")
	e.main0 = strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))

	// The worker's branch: code plus what the worker's close-out committed.
	e.branch = "worker/engineer-" + n + "-add-hello"
	gittest.Run(t, clone, "checkout", "-b", e.branch)
	write("hello.go", "package main\nfunc Hello(name string) string {\n\treturn \"hi\"\n}\n")
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "feat: Say hello (#"+n+")")
	write(".ard/"+n+"/plan.md", "## Implementation\nAdd hello.go.\n")
	write(".ard/"+n+"/worker-engineer.md", "---\nrole: engineer\nstatus: done\nattempt: 1\n---\nAdded hello.\n")
	gittest.Run(t, clone, "add", "-A")
	gittest.Run(t, clone, "commit", "-m", "ard(worker:engineer): #"+n+" Added hello")
	gittest.Run(t, clone, "push", "origin", "HEAD:refs/heads/"+e.branch)
	e.tip = strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	gittest.Run(t, clone, "checkout", "main")
	e.prN = gh.SeedPR(github.PullRequest{Title: "feat: Say hello (#" + n + ")", Author: "ard[bot]", State: github.StateOpen, Draft: true,
		HeadRef: e.branch, HeadSHA: e.tip, BaseRef: "main", Labels: []string{labels.Request(labels.StageReview, "eng-review")}})

	rolesRoot := t.TempDir()
	dir := ".ard/reviewers/eng-review"
	if err := os.MkdirAll(filepath.Join(rolesRoot, dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rolesRoot, dir, "prompt.txt"), []byte("prompt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.cfg = &config.Resolved{Repo: rolesRoot, SHA: e.main0, MaxBudgetUSD: 5, MaxPods: 3, Intake: config.Intake{Mode: config.IntakeLabel},
		Roles: []config.Role{{Kind: config.KindReviewer, Name: "eng-review", Dir: dir, Agent: "claude", Timeout: 10 * time.Minute}}}
	w.cfg.Store(e.cfg)
	w.pool.Store(pool.New(pool.Options{Max: 3}))

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
	e.loop = reviewer.New(reviewer.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store, Executor: ex, Finisher: fin, Git: gitx.New(""),
		Pool:   func() reviewer.Offerer { return w.Pool() },
		Agents: reg, Parse: w.parseFunc(), Model: w.Model,
		Repo: w.Repo, RepoDir: w.Root, RemoteURL: w.RemoteURL, Poll: 20 * time.Millisecond, Log: w.Log,
	})
	return e
}

func (e *reviewE2E) tick() {
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

func (e *reviewE2E) reviews() []github.ReviewInput {
	var out []github.ReviewInput
	for _, c := range e.gh.Writes() {
		if c.Method == "CreateReview" {
			out = append(out, c.Args[1].(github.ReviewInput))
		}
	}
	return out
}

func (e *reviewE2E) prLabels() []string {
	is, err := e.gh.GetIssue(context.Background(), e.prN)
	if err != nil {
		e.t.Fatal(err)
	}
	return is.Labels
}

func has(ls []string, want string) bool {
	for _, l := range ls {
		if l == want {
			return true
		}
	}
	return false
}

// The reviewer loop must use the one executor and Finisher the Reaper uses.
func TestReviewerLoopSharesParts(t *testing.T) {
	w := newTestWiring(t)
	l := reviewerLoop(w)
	if l == nil {
		t.Fatal("no reviewer loop")
	}
	rl, ok := l.(*reviewer.Loop)
	if !ok {
		t.Fatalf("loop is %T", l)
	}
	ex, _ := w.newExecutor()
	fin, _ := w.finisher()
	if got := field(t, rl, "d", "Executor"); got != any(ex) {
		t.Errorf("executor %v is not the shared one", got)
	}
	if got := field(t, rl, "d", "Finisher"); got != any(fin) {
		t.Errorf("finisher %v is not the shared one", got)
	}
}

func TestReviewEndToEndInlineComments(t *testing.T) {
	e := newReviewE2E(t, reviewerAgent)
	e.tick()
	n := itoa(e.issueN)

	rs := e.reviews()
	if len(rs) != 1 {
		t.Fatalf("reviews = %d", len(rs))
	}
	r := rs[0]
	if r.CommitSHA != e.tip {
		t.Errorf("review commit = %s, want the pinned %s", r.CommitSHA, e.tip)
	}
	if len(r.Comments) != 1 || r.Comments[0].Path != "hello.go" || r.Comments[0].Line != 2 || r.Comments[0].Side != "RIGHT" ||
		!strings.HasPrefix(r.Comments[0].Body, "**high · blocking**") {
		t.Errorf("inline comments = %+v", r.Comments)
	}
	if !strings.HasPrefix(r.Body, "<!-- ard:closeout job=") || !strings.Contains(r.Body, "README.md:1") || !strings.Contains(r.Body, "One blocking problem.") {
		t.Errorf("body:\n%s", r.Body)
	}

	// The branch gained one commit: the role file and the redacted log. No [skip ci].
	subjects := strings.Split(strings.TrimSpace(gittest.Run(t, e.bare, "log", "--format=%s", e.tip+".."+e.branch)), "\n")
	if len(subjects) != 1 || !strings.HasPrefix(subjects[0], "ard(review:eng-review): #"+n+" ") || strings.Contains(subjects[0], "skip ci") {
		t.Fatalf("subjects = %q", subjects)
	}
	if msg := gittest.Run(t, e.bare, "log", "-1", "--format=%B", e.branch); strings.Contains(msg, "skip ci") {
		t.Errorf("commit message carries [skip ci]:\n%s", msg)
	}
	var logs, costs int
	for _, f := range strings.Fields(gittest.Run(t, e.bare, "diff", "--name-only", e.tip, e.branch)) {
		switch {
		case f == ".ard/"+n+"/review-eng-review.md":
		case strings.HasPrefix(f, ".ard/"+n+"/costs/") && strings.HasSuffix(f, ".json"):
			costs++ // the controller's usage record rides the PR branch
		case strings.HasPrefix(f, ".ard/"+n+"/logs/"):
			logs++
			if b := gittest.Run(t, e.bare, "show", e.branch+":"+f); strings.Contains(b, e2eToken) || !strings.Contains(b, "using token") {
				t.Errorf("committed log %s must hold the redacted line, not the token", f)
			}
		default:
			t.Errorf("the review commit changed %s", f)
		}
	}
	if logs != 1 || costs != 1 {
		t.Errorf("logs = %d, usage records = %d", logs, costs)
	}
	if got := gittest.Run(t, e.bare, "show", e.branch+":hello.go"); strings.Contains(got, "tampered") {
		t.Error("the agent's code edit was committed")
	}
	if got := strings.TrimSpace(gittest.Run(t, e.bare, "rev-parse", "main")); got != e.main0 {
		t.Errorf("main moved: %s", got)
	}

	ls := e.prLabels()
	if !has(ls, labels.Changes("eng-review")) || has(ls, labels.Running(labels.StageReview, "eng-review")) || has(ls, labels.Done(labels.StageReview, "eng-review")) {
		t.Errorf("PR labels = %v", ls)
	}
	if cs, _ := e.gh.ListComments(context.Background(), e.issueN, time.Time{}); len(cs) != 0 {
		t.Errorf("comments on the issue: %+v", cs)
	}
	if prs, _ := e.gh.ListPRs(context.Background(), github.ListPRsOpts{State: github.StateOpen}); len(prs) != 1 || !prs[0].Draft {
		t.Errorf("the loop changed the PR: %+v", prs)
	}
}

func TestReviewEndToEndBodyFallback(t *testing.T) {
	e := newReviewE2E(t, reviewerAgent)
	e.gh.FailNext("CreateReview", &github.HTTPError{Status: 422, Message: "Unprocessable"})
	e.tick()
	rs := e.reviews()
	if len(rs) != 2 {
		t.Fatalf("CreateReview calls = %d", len(rs))
	}
	if len(rs[0].Comments) != 1 {
		t.Errorf("first call = %+v", rs[0])
	}
	if len(rs[1].Comments) != 0 || !strings.Contains(rs[1].Body, "hello.go:2") || !strings.Contains(rs[1].Body, "README.md:1") {
		t.Errorf("second call = %+v", rs[1])
	}
	if ls := e.prLabels(); !has(ls, labels.Changes("eng-review")) {
		t.Errorf("PR labels = %v", ls)
	}
}
