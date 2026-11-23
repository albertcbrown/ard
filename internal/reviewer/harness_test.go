package reviewer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
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
	"github.com/albertcbrown/ard/internal/reaper"
	"github.com/albertcbrown/ard/internal/state"
)

var (
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	t0    = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

const (
	self     = "ard[bot]"
	tokenVal = "ard-oauth-test-value-0123456789abcdef"
	tipSHA   = "1111111111111111111111111111111111111111"
	baseSHA  = "2222222222222222222222222222222222222222"
	reviewer = "eng-review"
)

// fakeEx is an executor the test scripts.
type fakeEx struct {
	mu         sync.Mutex
	prepared   []executor.Job
	started    []executor.Command
	prepareErr error
	startErr   error
	running    chan struct{} // when non-nil, Status is Running until it is closed
	exit       int
}

func (f *fakeEx) Prune(context.Context, time.Duration) error { return nil }

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
	return executor.Handle{JobID: j.ID, Kind: "host", Issue: j.Issue, Stage: j.Stage, Role: j.Role, Worktree: "/wt", BaseSHA: j.Ref,
		View: executor.Paths{Work: "/wt", Role: "/role", ConfigDir: "/home/.claude"}}, nil
}

func (f *fakeEx) Start(_ context.Context, h executor.Handle, c executor.Command) (executor.Handle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return h, f.startErr
	}
	f.started = append(f.started, c)
	return h, nil
}

func (f *fakeEx) Status(context.Context, executor.Handle) (executor.Status, error) {
	f.mu.Lock()
	ch := f.running
	f.mu.Unlock()
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
	Opts  reaper.FinishOptions
}

type fakeFin struct {
	mu    sync.Mutex
	calls []finishCall
	err   error
	after func() // runs once the call is recorded, to script the close-out's labels
}

func (f *fakeFin) FinishWith(_ context.Context, id string, h executor.Handle, _ executor.Status, _ agent.Result, o reaper.FinishOptions) (closeout.Outcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, finishCall{id, h, o})
	if f.after != nil {
		f.after()
	}
	return closeout.OutcomeChanges, f.err
}

func (f *fakeFin) got() []finishCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]finishCall(nil), f.calls...)
}

// fakeGit scripts the read-only git calls.
type fakeGit struct {
	mu         sync.Mutex
	fetches    []string
	tips       []string // successive RevParse answers; the last repeats
	fetchErr   error
	mbErr      error
	diffErr    error
	patch      string
	truncated  bool
	added      map[string][]gitx.LineRange
	changed    []string          // CodeChanged answer
	files      map[string]string // FileAt answers, by "rev:path"
	changedEx  []string
	changedErr error
	diffCalls  [][]string
	revs       int
}

func (g *fakeGit) FetchRef(_ context.Context, _, _, spec string, _ gitx.TokenFunc) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fetches = append(g.fetches, spec)
	return g.fetchErr
}

func (g *fakeGit) RevParse(context.Context, string, string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	i := g.revs
	g.revs++
	if i >= len(g.tips) {
		i = len(g.tips) - 1
	}
	return g.tips[i], nil
}

func (g *fakeGit) MergeBase(context.Context, string, string, string) (string, error) {
	return baseSHA, g.mbErr
}

func (g *fakeGit) Diff(_ context.Context, _, from, to string, max int64, ex ...string) ([]byte, bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.diffCalls = append(g.diffCalls, append([]string{from, to}, ex...))
	return []byte(g.patch), g.truncated, g.diffErr
}

func (g *fakeGit) AddedLinesBetween(context.Context, string, string, string, ...string) (map[string][]gitx.LineRange, error) {
	return g.added, nil
}

func (g *fakeGit) CodeChanged(_ context.Context, _, _, _ string, ex ...string) ([]string, error) {
	g.changedEx = ex
	return g.changed, g.changedErr
}

func (g *fakeGit) FileAt(_ context.Context, _, rev, path string, max int64) ([]byte, bool, error) {
	v, ok := g.files[rev+":"+path]
	if ok && int64(len(v)) > max { // the real FileAt errors on an oversize blob
		return nil, false, fmt.Errorf("%s: %d bytes exceeds %d", path, len(v), max)
	}
	return []byte(v), ok, nil
}

func (g *fakeGit) nFetches() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.fetches)
}

type harness struct {
	t      *testing.T
	gh     *fake.Fake
	fx     effects.Effects
	store  *state.Store
	ex     *fakeEx
	fin    *fakeFin
	git    *fakeGit
	pool   *pool.Pool
	cfg    *config.Resolved
	loop   *Loop
	stop   chan struct{}
	dry    bool
	noCred bool
	ledger string
}

type option func(*harness)

func dryRun(h *harness) { h.dry = true }
func noCred(h *harness) { h.noCred = true }

func newHarness(t *testing.T, opts ...option) *harness {
	t.Helper()
	h := &harness{t: t, ex: &fakeEx{}, fin: &fakeFin{}, stop: make(chan struct{}),
		git: &fakeGit{tips: []string{tipSHA}, patch: " a.go | 1 +\n", added: map[string][]gitx.LineRange{"a.go": {{From: 1, To: 1}}}},
		gh:  fake.New(fake.WithIdentity(github.Identity{Login: self, ID: 7, Kind: github.AuthApp}), fake.WithClock(func() time.Time { return time.Now() }))}
	role := func(name string) config.Role {
		return config.Role{Kind: config.KindReviewer, Name: name, Dir: ".ard/reviewers/" + name, Agent: "claude", Timeout: 30 * time.Minute, Sidecar: ""}
	}
	h.cfg = &config.Resolved{Repo: t.TempDir(), SHA: "abc123", MaxBudgetUSD: 5, MaxPods: 3,
		Intake: config.Intake{Mode: config.IntakeLabel},
		Roles:  []config.Role{role("sec-review"), role(reviewer)}}
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
	h.pool = pool.New(pool.Options{Max: 3})
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
		Reader: h.gh, Effects: h.fx, Store: h.store, Executor: h.ex, Finisher: h.fin, Git: h.git,
		Pool: func() Offerer { return h.pool }, Agents: reg, Model: cred,
		Repo: github.Repo{Owner: "o", Name: "r"}, RepoDir: t.TempDir(), RemoteURL: "file:///remote", Poll: 5 * time.Millisecond, Log: quiet,
	})
	return h
}

func (h *harness) tickAsync() (*controller.Inflight, error) {
	in := controller.NewInflight(context.Background(), quiet)
	err := h.loop.Tick(context.Background(), controller.Env{Log: quiet, Cfg: h.cfg, Inflight: in, Stopping: h.stop})
	return in, err
}

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

// scenario seeds an ard-authored PR for issue n on worker/engineer-<n>-fix,
// requesting the reviewer, and the open scanner-style issue. Mutators adjust
// the PR or the issue before they are seeded.
type scenario struct {
	pr    github.PullRequest
	issue github.Issue
}

func (h *harness) scenario(mut ...func(*scenario)) (pr, issue int) {
	h.t.Helper()
	s := &scenario{
		issue: github.Issue{Title: "Fix it", Body: "It is broken.", Author: self, CreatedAt: t0, UpdatedAt: t0},
		pr: github.PullRequest{Title: "ard(worker:engineer): fix it", Author: self, State: github.StateOpen, HeadRef: "", HeadSHA: tipSHA, BaseRef: "main",
			Labels: []string{labels.Request(labels.StageReview, reviewer)}, Draft: true},
	}
	for _, m := range mut {
		m(s)
	}
	issue = h.gh.SeedIssue(s.issue)
	if s.pr.HeadRef == "" {
		s.pr.HeadRef = "worker/engineer-" + strconv.Itoa(issue) + "-fix-it"
	}
	pr = h.gh.SeedPR(s.pr)
	return pr, issue
}

func (h *harness) prLabels(n int) []string {
	h.t.Helper()
	is, err := h.gh.GetIssue(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return is.Labels
}

func (h *harness) has(n int, label string) bool { return hasLabel(h.prLabels(n), label) }

func readLedger(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

var errBoom = errors.New("boom")
