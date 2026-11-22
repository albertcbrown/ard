package servecmd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/gitx/gittest"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
	"github.com/albertcbrown/ard/internal/triage"
)

const e2eToken = "ard-oauth-test-value-0123456789abcdef"

// A fake claude: it needs the untrusted snapshot and the credential, writes the
// role file only (close-out allows the lead nothing else), leaks the token on stderr, and prints a result event.
const goodAgent = `#!/bin/sh
test -f .ard-input/*/issue.md || { echo "no snapshot" >&2; exit 3; }
test -n "$CLAUDE_CODE_OAUTH_TOKEN" || exit 4
for d in .ard-input/*; do n=${d##*/}; done
echo "using token $CLAUDE_CODE_OAUTH_TOKEN" >&2
mkdir -p ".ard/$n"
cat > ".ard/$n/triage-lead.md" <<'EOF2'
---
role: lead
status: done
attempt: 1
next: [triage:architect]
worker: engineer
---
Routed to the architect, then the engineer.
EOF2
printf '%s\n' '{"type":"result","subtype":"success","is_error":false,"duration_ms":10,"num_turns":1,"total_cost_usd":0.01,"session_id":"s","result":"ok"}'
`

// meddlingAgent is goodAgent plus a write outside the lead's allowlist.
var meddlingAgent = strings.Replace(goodAgent, "printf '%s\\n'", "echo \"# Plan\" >> \".ard/$n/plan.md\"\nprintf '%s\\n'", 1)

const badAgent = `#!/bin/sh
echo "something went wrong" >&2
exit 1
`

type e2e struct {
	t      *testing.T
	w      *wiring
	gh     *fake.Fake
	bare   string
	cfg    *config.Resolved
	loop   *triage.Loop
	issueN int
}

func newE2E(t *testing.T, script string) *e2e {
	t.Helper()
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", e2eToken)
	w := newTestWiring(t)
	w.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	stage, err := filepath.EvalSymlinks(t.TempDir()) // the committer refuses a symlinked staging path
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
	sha := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))

	rolesRoot := t.TempDir()
	role := func(kind config.Kind, name, dir string) config.Role {
		if err := os.MkdirAll(filepath.Join(rolesRoot, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(rolesRoot, dir, "prompt.txt"), []byte("prompt\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return config.Role{Kind: kind, Name: name, Dir: dir, Agent: "claude", Timeout: time.Minute}
	}
	cfg := &config.Resolved{Repo: rolesRoot, SHA: sha, MaxBudgetUSD: 5, MaxPods: 3, Intake: config.Intake{Mode: config.IntakeLabel},
		Roles: []config.Role{role(config.KindTriage, "architect", ".ard/triage/architect"), role(config.KindTriage, "lead", ".ard/triage/lead"),
			role(config.KindWorker, "engineer", ".ard/workers/engineer")}}
	w.cfg.Store(cfg)
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
	loop := triage.New(triage.Deps{
		Reader: w.Reader, Effects: w.Effects, Store: w.Store, Executor: ex, Finisher: fin,
		Pool: func() triage.Offerer { return w.Pool() }, Agents: reg, Parse: w.parseFunc(), Model: w.Model,
		Repo: w.Repo.String(), RepoDir: w.Root, Poll: 20 * time.Millisecond, Log: w.Log,
	})
	e := &e2e{t: t, w: w, gh: gh, bare: bare, cfg: cfg, loop: loop}
	e.issueN = gh.SeedIssue(github.Issue{Title: "Crash on start", Body: "It crashes.", Author: "alice", Labels: []string{labels.Pending}, CreatedAt: time.Now().Add(-time.Hour)})
	gh.SeedTimeline(e.issueN, github.TimelineEvent{Event: "labeled", Label: labels.Pending, Actor: "maint", CreatedAt: time.Now().Add(-30 * time.Minute)})
	gh.SetPermission("maint", github.Permission{Role: "maintain"})
	return e
}

func (e *e2e) tick() {
	e.t.Helper()
	ctx := context.Background()
	in := controller.NewInflight(ctx, e.w.Log)
	if err := e.loop.Tick(ctx, controller.Env{Cfg: e.cfg, Log: e.w.Log, Inflight: in}); err != nil {
		e.t.Fatalf("tick: %v", err)
	}
	select {
	case <-in.Wait():
	case <-time.After(60 * time.Second):
		e.t.Fatal("watchers did not finish")
	}
}

func (e *e2e) labels() []string {
	is, err := e.gh.GetIssue(context.Background(), e.issueN)
	if err != nil {
		e.t.Fatal(err)
	}
	return is.Labels
}

func (e *e2e) has(label string) bool {
	for _, l := range e.labels() {
		if l == label {
			return true
		}
	}
	return false
}

func (e *e2e) comments() []github.Comment {
	cs, err := e.gh.ListComments(context.Background(), e.issueN, time.Time{})
	if err != nil {
		e.t.Fatal(err)
	}
	return cs
}

func (e *e2e) lastJob() state.Job {
	e.t.Helper()
	// There is no list-all call; the job ids are in the claim events.
	evs, err := e.w.Store.Events(context.Background(), state.EventFilter{Kind: state.EventClaim})
	if err != nil || len(evs) == 0 {
		e.t.Fatalf("no claim events: %v", err)
	}
	last, err := e.w.Store.Job(context.Background(), evs[len(evs)-1].JobID)
	if err != nil {
		e.t.Fatal(err)
	}
	return last
}

func TestTriageEndToEndDone(t *testing.T) {
	e := newE2E(t, goodAgent)
	e.tick()
	n := e.issueN
	for _, want := range []string{labels.Done(labels.StageTriage, "lead"), labels.Request(labels.StageTriage, "architect")} {
		if !e.has(want) {
			t.Errorf("missing %s; labels %v", want, e.labels())
		}
	}
	for _, gone := range []string{labels.Pending, labels.Running(labels.StageTriage, "lead"), labels.ExecutorFailed} {
		if e.has(gone) {
			t.Errorf("%s still set", gone)
		}
	}
	if cs := e.comments(); len(cs) != 1 {
		t.Errorf("comments = %d, want one close-out comment", len(cs))
	}
	files := strings.Fields(gittest.Run(t, e.bare, "ls-tree", "-r", "--name-only", "main"))
	var gotRole, gotLog bool
	for _, f := range files {
		switch {
		case f == ".ard/"+itoa(n)+"/triage-lead.md":
			gotRole = true
		case f == ".ard/"+itoa(n)+"/plan.md":
			t.Errorf("the lead's plan.md was committed")
		case strings.HasPrefix(f, ".ard/"+itoa(n)+"/logs/triage-lead-") && strings.HasSuffix(f, ".log"):
			gotLog = true
			if b := gittest.Run(t, e.bare, "show", "main:"+f); strings.Contains(b, e2eToken) || !strings.Contains(b, "using token") {
				t.Errorf("the committed log must hold the redacted line, not the token:\n%s", b)
			}
		case strings.HasPrefix(f, ".ard/"+itoa(n)+"/"):
			t.Errorf("unexpected file %s", f)
		case f == ".git" || strings.Contains(f, ".ard-input"):
			t.Errorf("untrusted input committed: %s", f)
		}
	}
	if !gotRole || !gotLog {
		t.Errorf("committed files = %v", files)
	}
	for _, f := range files {
		if strings.Contains(f, ".ard-input") {
			t.Errorf("committed %s", f)
		}
	}
	j := e.lastJob()
	if j.Status != state.StatusDone {
		t.Errorf("job status = %s (%s)", j.Status, j.FailReason)
	}
	var h executor.Handle
	if err := json.Unmarshal(j.Handle, &h); err != nil || h.Worktree == "" {
		t.Fatalf("handle = %s, %v", j.Handle, err)
	}
	if _, err := os.Stat(h.Worktree); !os.IsNotExist(err) {
		t.Errorf("worktree not removed: %v", err)
	}
	if a, err := e.w.Store.Admission(context.Background(), n); err != nil || a.LabeledBy != "maint" || a.JobID != j.ID {
		t.Errorf("admission = %+v, %v", a, err)
	}
}

func TestTriageEndToEndWriteDenied(t *testing.T) {
	e := newE2E(t, meddlingAgent)
	e.tick()
	n := e.issueN
	if !e.has(labels.NeedsHuman) || e.has(labels.Done(labels.StageTriage, "lead")) || e.has(labels.Request(labels.StageTriage, "architect")) {
		t.Errorf("labels = %v", e.labels())
	}
	for _, f := range strings.Fields(gittest.Run(t, e.bare, "ls-tree", "-r", "--name-only", "main")) {
		if strings.HasPrefix(f, ".ard/"+itoa(n)+"/") && !strings.Contains(f, "/logs/") {
			t.Errorf("committed %s, want only the log", f)
		}
	}
	if cs := e.comments(); len(cs) != 1 || !strings.Contains(cs[0].Body, "write not allowed") {
		t.Errorf("comments = %+v", cs)
	}
}

func TestTriageEndToEndAgentFails(t *testing.T) {
	e := newE2E(t, badAgent)
	e.tick()
	if !e.has(labels.ExecutorFailed) || !e.has(labels.Pending) || e.has(labels.Running(labels.StageTriage, "lead")) {
		t.Fatalf("labels = %v", e.labels())
	}
	cs := e.comments()
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "something went wrong") {
		t.Errorf("comments = %+v", cs)
	}
	if j := e.lastJob(); j.Status != state.StatusFailed {
		t.Errorf("job status = %s", j.Status)
	}
}

// A clone failure hands off to one automatic retry; a second one is failed.
func TestTriageEndToEndCloneFailureRetriesOnce(t *testing.T) {
	e := newE2E(t, goodAgent)
	e.cfg.SHA = strings.Repeat("1", 40) // not in the clone: "resolve ref" fails with executor.ErrClone
	e.tick()
	if e.has(labels.ExecutorFailed) || !e.has(labels.Pending) || e.has(labels.Running(labels.StageTriage, "lead")) {
		t.Fatalf("after the first failure: labels %v", e.labels())
	}
	first := e.lastJob()
	if !first.AutoRetry {
		t.Error("the first failure did not hand off to a retry")
	}
	e.tick()
	second := e.lastJob()
	if second.ID == first.ID || second.RetryOf != first.ID {
		t.Fatalf("the second claim is not the linked retry: %+v", second)
	}
	if !e.has(labels.ExecutorFailed) {
		t.Errorf("the second clone failure must be failed: labels %v", e.labels())
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
