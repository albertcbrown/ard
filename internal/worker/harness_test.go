package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/committer"
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
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state"
)

var (
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	t0    = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

const (
	self     = "ard[bot]"
	tokenVal = "ard-oauth-test-value-0123456789abcdef"
)

// script is what a fake agent or test step does in its worktree. It returns the
// exit code. kind is "agent", "fix" or "test".
type script func(kind string, j executor.Job, h executor.Handle) int

// fakeEx makes real detached worktrees of the harness clone, runs a script on
// Start and reports it as exited.
type fakeEx struct {
	t       *testing.T
	clone   string
	mu      sync.Mutex
	jobs    []executor.Job
	cmds    map[string]executor.Command
	exit    map[string]int
	clean   []string
	run     script
	status  func(id string) (executor.Status, bool) // overrides Status
	prepErr error
	seq     int
}

func (f *fakeEx) Prepare(_ context.Context, j executor.Job) (executor.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.prepErr != nil {
		return executor.Handle{}, f.prepErr
	}
	f.jobs = append(f.jobs, j)
	sha := strings.TrimSpace(gittest.Run(f.t, f.clone, "rev-parse", j.Ref))
	wt, err := filepath.EvalSymlinks(f.t.TempDir())
	if err != nil {
		f.t.Fatal(err)
	}
	wt = filepath.Join(wt, "wt")
	gittest.Run(f.t, f.clone, "worktree", "add", "--detach", wt, sha)
	for _, in := range j.Inputs {
		p := filepath.Join(wt, in.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			f.t.Fatal(err)
		}
		if err := os.WriteFile(p, in.Data, 0o644); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, s := range j.Scrub {
		_ = os.Remove(filepath.Join(wt, s))
	}
	f.seq++
	logp := filepath.Join(filepath.Dir(wt), fmt.Sprintf("worker-%s-2026010%dT030405Z.log", j.Role, f.seq))
	if err := os.WriteFile(logp, []byte("log of "+j.ID+"\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
	return executor.Handle{JobID: j.ID, Kind: "host", Issue: j.Issue, Stage: j.Stage, Role: j.Role, Worktree: wt, BaseSHA: sha,
		LogPath: logp, Scrubbed: j.Scrub, View: executor.Paths{Work: wt, Role: "/role", ConfigDir: "/home/.claude"}}, nil
}

// kindOf names a step by its id: a 26-character ULID plus t<k> or f<k>.
func kindOf(id string) string {
	if len(id) > 26 {
		switch id[26] {
		case 't':
			return "test"
		case 'f':
			return "fix"
		}
	}
	return "agent"
}

func (f *fakeEx) Start(_ context.Context, h executor.Handle, c executor.Command) (executor.Handle, error) {
	f.mu.Lock()
	if f.cmds == nil {
		f.cmds, f.exit = map[string]executor.Command{}, map[string]int{}
	}
	f.cmds[h.JobID] = c
	var j executor.Job
	for _, x := range f.jobs {
		if x.ID == h.JobID {
			j = x
		}
	}
	run := f.run
	f.mu.Unlock()
	code := 0
	if run != nil {
		code = run(kindOf(h.JobID), j, h)
	}
	f.mu.Lock()
	f.exit[h.JobID] = code
	f.mu.Unlock()
	h.PID = 1
	return h, nil
}

func (f *fakeEx) Status(_ context.Context, h executor.Handle) (executor.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.status != nil {
		if st, ok := f.status(h.JobID); ok {
			return st, nil
		}
	}
	return executor.Status{State: executor.Exited, ExitCode: f.exit[h.JobID]}, nil
}

func (f *fakeEx) Logs(context.Context, executor.Handle, executor.LogsOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeEx) Cleanup(_ context.Context, h executor.Handle, _ executor.CleanupOptions) error {
	f.mu.Lock()
	f.clean = append(f.clean, h.JobID)
	f.mu.Unlock()
	return nil
}

func (f *fakeEx) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, j := range f.jobs {
		out = append(out, j.ID)
	}
	return out
}

type finishCall struct {
	JobID string
	H     executor.Handle
	St    executor.Status
	Res   agent.Result
	Opts  reaper.FinishOptions
}

type fakeFin struct {
	mu    sync.Mutex
	calls []finishCall
}

func (f *fakeFin) FinishWith(_ context.Context, id string, h executor.Handle, st executor.Status, res agent.Result, o reaper.FinishOptions) (closeout.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, finishCall{id, h, st, res, o})
	return closeout.OutcomeDone, nil
}

func (f *fakeFin) got() []finishCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]finishCall(nil), f.calls...)
}

// fakeCode records code commits and returns the harness's spare commits in order.
type fakeCode struct {
	mu   sync.Mutex
	reqs []committer.CodeRequest
	br   []string
	shas []string
	err  error
	// files holds the content of each request's files at commit time.
	files []map[string]string
	// before runs when a commit is requested, before its sources are read.
	before func()
}

func (f *fakeCode) CommitCode(_ context.Context, branch string, req committer.CodeRequest) (committer.Result, error) {
	if f.before != nil {
		f.before()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return committer.Result{}, f.err
	}
	got := map[string]string{}
	for _, cf := range req.Files {
		if cf.Delete {
			got[cf.Path] = "<deleted>"
			continue
		}
		b, err := os.ReadFile(cf.Src)
		if err != nil {
			return committer.Result{}, err
		}
		got[cf.Path] = string(b)
	}
	f.files = append(f.files, got)
	f.reqs = append(f.reqs, req)
	f.br = append(f.br, branch)
	sha := f.shas[len(f.reqs)-1]
	return committer.Result{SHA: sha, Attempts: 1}, nil
}

func (f *fakeCode) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

type countPool struct {
	*pool.Pool
	mu     sync.Mutex
	offers int
	last   []pool.Candidate
}

func (c *countPool) Offer(loop string, ttl time.Duration, cands []pool.Candidate) []pool.Decision {
	c.mu.Lock()
	c.offers++
	c.last = cands
	c.mu.Unlock()
	return c.Pool.Offer(loop, ttl, cands)
}

type harness struct {
	t     *testing.T
	gh    *fake.Fake
	fx    effects.Effects
	store *state.Store
	ex    *fakeEx
	fin   *fakeFin
	code  *fakeCode
	pool  *countPool
	cfg   *config.Resolved
	loop  *Loop
	stop  chan struct{}
	bare  string
	clone string
	now   time.Time
	dry   bool
	// ledger is the dry-run ledger path ("" when live).
	ledger string
	// log, when set, receives the loop's log records instead of quiet.
	log *slog.Logger
}

type option func(*harness)

func dryRun(h *harness) { h.dry = true }

func newHarness(t *testing.T, opts ...option) *harness {
	t.Helper()
	bare, clone := gittest.NewRemote(t)
	h := &harness{t: t, stop: make(chan struct{}), bare: bare, clone: clone, now: t0,
		gh: fake.New(fake.WithIdentity(github.Identity{Login: self, ID: 7, Kind: github.AuthApp}), fake.WithClock(func() time.Time { return time.Now() }))}
	h.fin, h.code = &fakeFin{}, &fakeCode{}
	// Spare commits the fake code lane "pushes".
	for i := 0; i < 6; i++ {
		gittest.Run(t, clone, "commit", "--allow-empty", "-m", fmt.Sprintf("spare %d", i))
		h.code.shas = append(h.code.shas, strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD")))
	}
	gittest.Run(t, clone, "reset", "--hard", "HEAD~6")
	sha := strings.TrimSpace(gittest.Run(t, clone, "rev-parse", "HEAD"))
	h.ex = &fakeEx{t: t, clone: clone}
	role := func(kind config.Kind, name, dir string) config.Role {
		return config.Role{Kind: kind, Name: name, Dir: dir, Agent: "claude", Timeout: 30 * time.Minute}
	}
	eng := role(config.KindWorker, "engineer", ".ard/workers/engineer")
	eng.Tests, eng.MaxTestFixes, eng.Reviewers = "sh check.sh", 2, []string{"eng-review"}
	eng.DiffWarnLines, eng.DiffMaxLines = 1000, 2000
	h.cfg = &config.Resolved{Repo: t.TempDir(), SHA: sha, MaxBudgetUSD: 5, MaxPods: 3, RequireApproval: true,
		Intake: config.Intake{Mode: config.IntakeLabel},
		Roles: []config.Role{eng,
			role(config.KindReviewer, "eng-review", ".ard/reviewers/eng-review"),
			role(config.KindReviewer, "license-review", ".ard/reviewers/license-review"),
			role(config.KindReviewer, "sec-review", ".ard/reviewers/sec-review")}}
	for _, o := range opts {
		o(h)
	}
	st, err := state.Open(context.Background(), t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h.store = st
	if h.dry {
		h.ledger = filepath.Join(t.TempDir(), "effects.jsonl")
		d, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet, LedgerPath: h.ledger})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Close() })
		h.fx = d
	} else {
		h.fx = effects.NewLive(h.gh, gitx.New(""), quiet)
	}
	h.pool = &countPool{Pool: pool.New(pool.Options{Max: 3})}
	cred, err := modelauth.Resolve("", claude.New(claude.Options{}).CredentialEnv, func(k string) string {
		if k == "CLAUDE_CODE_OAUTH_TOKEN" {
			return tokenVal
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := agent.NewRegistry(claude.New(claude.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	h.loop = New(Deps{
		Reader: h.gh, Effects: h.fx, Store: h.store, Executor: h.ex, Finisher: h.fin, Code: h.code, Git: gitx.New(""),
		Pool: func() Offerer { return h.pool }, Agents: reg, Model: cred,
		Secrets: func() *redact.Secrets { s := redact.NewSecrets(); _ = s.Add("MODEL_KEY", tokenVal); return s },
		Repo:    github.Repo{Owner: "o", Name: "r"}, RepoDir: clone, RemoteURL: "file://" + bare,
		Poll: 2 * time.Millisecond, Now: func() time.Time { return h.now }, Log: quiet,
	})
	return h
}

func (h *harness) tickAsync() (*controller.Inflight, error) {
	lg := quiet
	if h.log != nil {
		lg = h.log
	}
	in := controller.NewInflight(context.Background(), lg)
	err := h.loop.Tick(context.Background(), controller.Env{Log: lg, Cfg: h.cfg, Inflight: in, Stopping: h.stop})
	return in, err
}

func (h *harness) tick() error {
	h.t.Helper()
	in, err := h.tickAsync()
	select {
	case <-in.Wait():
	case <-time.After(30 * time.Second):
		h.t.Fatal("watchers did not finish")
	}
	return err
}

func (h *harness) mustTick() {
	h.t.Helper()
	if err := h.tick(); err != nil {
		h.t.Fatalf("tick: %v", err)
	}
}

// issue files an App-authored issue requesting the engineer, approved.
func (h *harness) issue(title string, lbls ...string) int {
	base := []string{labels.Request(labels.StageWorker, "engineer"), labels.Approved}
	return h.gh.SeedIssue(github.Issue{Title: title, Body: "b", Author: self, Labels: append(base, lbls...), CreatedAt: t0, UpdatedAt: t0})
}

func (h *harness) getIssue(n int) github.Issue {
	h.t.Helper()
	is, err := h.gh.GetIssue(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return is
}

func (h *harness) has(n int, label string) bool { return hasLabel(h.getIssue(n).Labels, label) }

func (h *harness) comments(n int) []github.Comment {
	cs, err := h.gh.ListComments(context.Background(), n, time.Time{})
	if err != nil {
		h.t.Fatal(err)
	}
	return cs
}

func (h *harness) prs() []github.PullRequest {
	ps, err := h.gh.ListPRs(context.Background(), github.ListPRsOpts{State: github.StateOpen})
	if err != nil {
		h.t.Fatal(err)
	}
	return ps
}

func (h *harness) jobs() []state.Job {
	js, err := h.store.RunningJobs(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return js
}

// seedDeferred stores a closed lead close-out that deferred these reviews.
func (h *harness) seedDeferred(n int, reviews ...string) {
	h.t.Helper()
	ctx := context.Background()
	sj, err := h.store.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "lead", Issue: n})
	if err != nil {
		h.t.Fatal(err)
	}
	var def []closeout.NextReq
	for _, r := range reviews {
		def = append(def, closeout.NextReq{Stage: "review", Role: r})
	}
	rec := closeout.Record{Version: 1, Phase: closeout.PhaseClosed, Job: closeout.Job{ID: sj.ID, Role: "lead", Stage: "triage"},
		Plan: closeout.Plan{Outcome: closeout.OutcomeDone, Deferred: def}}
	b, _ := json.Marshal(rec)
	if err := h.store.SetCloseout(ctx, sj.ID, "closed", b); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.Complete(ctx, sj.ID, nil); err != nil {
		h.t.Fatal(err)
	}
}

const roleDone = "---\nrole: engineer\nstatus: done\nattempt: 1\n---\nFixes #99 by adding hello.\n"

func roleFileOf(status string) string {
	return "---\nrole: engineer\nstatus: " + status + "\nattempt: 1\n---\nSummary.\n"
}

func writeFile(t *testing.T, wt, rel, content string) {
	t.Helper()
	p := filepath.Join(wt, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// agentWrites is a script whose agent steps write the files and a done role
// file, and whose test steps exit with testExit(k).
func (h *harness) agentWrites(files map[string]string, role string, testExit func(k int) int) {
	tk := 0
	h.ex.run = func(kind string, j executor.Job, hd executor.Handle) int {
		switch kind {
		case "test":
			tk++
			return testExit(tk)
		default:
			for p, c := range files {
				writeFile(h.t, hd.Worktree, p, c)
			}
			if role != "" {
				writeFile(h.t, hd.Worktree, fmt.Sprintf(".ard/%d/worker-engineer.md", j.Issue), strings.ReplaceAll(role, "@WT@", hd.Worktree))
			}
		}
		return 0
	}
}

var errBoom = errors.New("boom")
