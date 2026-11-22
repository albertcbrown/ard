package triage

import (
	"context"
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
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
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

// fakeEx is an executor whose Prepare errors and Status the test sets.
type fakeEx struct {
	mu         sync.Mutex
	prepared   []executor.Job
	started    []executor.Command
	prepareErr error
	startErr   error
	running    chan struct{} // when non-nil, Status is Running until it is closed
	exit       int
	gone       bool // Status reports Gone (the process vanished or was killed)
}

func (f *fakeEx) Prune(context.Context, time.Duration) error { return nil }

func (f *fakeEx) setGone() {
	f.mu.Lock()
	f.gone = true
	f.mu.Unlock()
}

func (f *fakeEx) nStarted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.started)
}

func (f *fakeEx) nPrepared() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.prepared)
}

func (f *fakeEx) Prepare(_ context.Context, j executor.Job) (executor.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prepared = append(f.prepared, j)
	if f.prepareErr != nil {
		return executor.Handle{}, f.prepareErr
	}
	return executor.Handle{JobID: j.ID, Kind: "host", Issue: j.Issue, Stage: j.Stage, Role: j.Role, Worktree: "/wt",
		View: executor.Paths{Work: "/wt", Role: "/role", ConfigDir: "/home/.claude"}}, nil
}

func (f *fakeEx) Start(_ context.Context, h executor.Handle, c executor.Command) (executor.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return h, f.startErr
	}
	f.started = append(f.started, c)
	h.PID = 4242
	return h, nil
}

func (f *fakeEx) Status(context.Context, executor.Handle) (executor.Status, error) {
	f.mu.Lock()
	ch, gone := f.running, f.gone
	f.mu.Unlock()
	if gone {
		return executor.Status{State: executor.Gone}, nil
	}
	if ch != nil {
		select {
		case <-ch:
		default:
			return executor.Status{State: executor.Running, Alive: true}, nil
		}
	}
	return executor.Status{State: executor.Exited, ExitCode: f.exit}, nil
}

func (f *fakeEx) Logs(context.Context, executor.Handle, executor.LogsOptions) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (f *fakeEx) Cleanup(context.Context, executor.Handle, executor.CleanupOptions) error { return nil }

type finishCall struct {
	JobID string
	H     executor.Handle
	Cause closeout.Cause
}

// fakeFin records Finish calls and can block them.
type fakeFin struct {
	mu    sync.Mutex
	calls []finishCall
	block chan struct{}
	err   error
}

func (f *fakeFin) Finish(_ context.Context, id string, h executor.Handle, _ executor.Status, _ agent.Result, c closeout.Cause) (closeout.Outcome, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, finishCall{id, h, c})
	return closeout.OutcomeDone, f.err
}

func (f *fakeFin) got() []finishCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]finishCall(nil), f.calls...)
}

// countPool counts Offer calls around a real pool.
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
	pool  *countPool
	cfg   *config.Resolved
	loop  *Loop
	stop  chan struct{}
	dry   bool
	// ledger is the dry-run ledger path ("" when live).
	ledger string
	// noCred leaves the model credential unset.
	noCred bool
}

type option func(*harness)

func dryRun(h *harness)  { h.dry = true }
func noCred(h *harness)  { h.noCred = true }
func allMode(h *harness) { h.cfg.Intake.Mode = config.IntakeAll }
func withLabel(l string) option {
	return func(h *harness) { h.cfg.Intake.Label = l }
}

func newHarness(t *testing.T, opts ...option) *harness {
	t.Helper()
	h := &harness{t: t, ex: &fakeEx{}, fin: &fakeFin{}, stop: make(chan struct{}),
		gh: fake.New(fake.WithIdentity(github.Identity{Login: self, ID: 7, Kind: github.AuthApp}), fake.WithClock(func() time.Time { return time.Now() }))}
	role := func(name string) config.Role {
		return config.Role{Kind: config.KindTriage, Name: name, Dir: ".ard/triage/" + name, Agent: "claude", Timeout: 30 * time.Minute}
	}
	h.cfg = &config.Resolved{Repo: t.TempDir(), SHA: "abc123", MaxBudgetUSD: 5, MaxPods: 3,
		Intake: config.Intake{Mode: config.IntakeLabel},
		Roles:  []config.Role{role("architect"), role("lead")}} // not in canonical order on purpose
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
	var cred modelauth.Credential
	if !h.noCred {
		cred, err = modelauth.Resolve("", claude.New(claude.Options{}).CredentialEnv, func(k string) string {
			if k == "CLAUDE_CODE_OAUTH_TOKEN" {
				return tokenVal
			}
			return ""
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	reg, err := agent.NewRegistry(claude.New(claude.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	h.loop = New(Deps{
		Reader: h.gh, Effects: h.fx, Store: h.store, Executor: h.ex, Finisher: h.fin,
		Pool: func() Offerer { return h.pool }, Agents: reg, Model: cred,
		Repo: "o/r", RepoDir: t.TempDir(), Poll: 5 * time.Millisecond, Log: quiet,
	})
	return h
}

// tickAsync runs one Tick and returns the Inflight holding its watchers.
func (h *harness) tickAsync() (*controller.Inflight, error) {
	in := controller.NewInflight(context.Background(), quiet)
	err := h.loop.Tick(context.Background(), controller.Env{Log: quiet, Cfg: h.cfg, Inflight: in, Stopping: h.stop})
	return in, err
}

// tick runs one Tick and waits for every watcher it started.
func (h *harness) tick() error {
	h.t.Helper()
	in, err := h.tickAsync()
	select {
	case <-in.Wait():
	case <-time.After(10 * time.Second):
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

// human files an issue by alice carrying lbls, labelled by actor at the given time.
func (h *harness) human(title, body string, lbls ...string) int {
	return h.gh.SeedIssue(github.Issue{Title: title, Body: body, Author: "alice", Labels: lbls, CreatedAt: t0, UpdatedAt: t0})
}

// labelledBy records a labeled event by actor on n and sets the actor's permission role.
func (h *harness) labelledBy(n int, label, actor, role string, at time.Time) {
	h.gh.SeedTimeline(n, github.TimelineEvent{Event: "labeled", Label: label, Actor: actor, CreatedAt: at})
	h.gh.SetPermission(actor, github.Permission{Role: role})
}

func (h *harness) issue(n int) github.Issue {
	h.t.Helper()
	is, err := h.gh.GetIssue(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return is
}

func (h *harness) has(n int, label string) bool { return hasLabel(h.issue(n).Labels, label) }

func (h *harness) comments(n int) []github.Comment {
	cs, err := h.gh.ListComments(context.Background(), n, time.Time{})
	if err != nil {
		h.t.Fatal(err)
	}
	return cs
}

func (h *harness) jobs() []state.Job {
	js, err := h.store.RunningJobs(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	return js
}

func readLedger(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

var lead = labels.Pending

func executorHandle() executor.Handle {
	return executor.Handle{JobID: "J", View: executor.Paths{Work: "/wt", Role: "/role", ConfigDir: "/home/.claude"}}
}
