package reaper

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
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state"
)

var (
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	t0    = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

const secretValue = "s3cr3t-value-0123456789"

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }

// fakeExec is an executor whose statuses the test sets. Cleanup of a live job
// is a kill: the job is Gone afterwards, as in the local executor, whose
// tracker is dropped.
type fakeExec struct {
	mu       sync.Mutex
	st       map[string]executor.Status
	cleanups []cleanup
	pruned   []time.Duration
	err      error // returned by Cleanup when set
}

type cleanup struct {
	ID   string
	Keep bool
}

func (f *fakeExec) Status(_ context.Context, h executor.Handle) (executor.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.st[h.JobID]; ok {
		return s, nil
	}
	return executor.Status{State: executor.Gone}, nil
}

func (f *fakeExec) Cleanup(_ context.Context, h executor.Handle, o executor.CleanupOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.cleanups = append(f.cleanups, cleanup{h.JobID, o.KeepWorktree})
	if s := f.st[h.JobID]; s.State == executor.Running || s.State == executor.Orphaned {
		s.State, s.Alive = executor.Gone, false
		f.st[h.JobID] = s
	}
	if !o.KeepWorktree && h.Worktree != "" {
		os.RemoveAll(h.Worktree)
	}
	return nil
}

func (f *fakeExec) Prune(_ context.Context, d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruned = append(f.pruned, d)
	return nil
}

func (f *fakeExec) cleanupsOf(id string) []cleanup {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []cleanup
	for _, c := range f.cleanups {
		if c.ID == id {
			out = append(out, c)
		}
	}
	return out
}

type fakeCommitter struct {
	mu   sync.Mutex
	err  error
	reqs []committer.Request
	brs  []string
}

func (c *fakeCommitter) Commit(_ context.Context, branch string, req committer.Request) (committer.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return committer.Result{}, c.err
	}
	c.reqs = append(c.reqs, req)
	c.brs = append(c.brs, branch)
	return committer.Result{SHA: fmt.Sprintf("%040d", len(c.reqs))}, nil
}

type harness struct {
	t        *testing.T
	clk      *clock
	gh       *fake.Fake
	fx       effects.Effects
	store    *state.Store
	ex       *fakeExec
	cm       *fakeCommitter
	closer   *closeout.Closer
	fin      *Finisher
	r        *Reaper
	pool     *pool.Pool
	cfg      *config.Resolved
	dir      string
	results  map[string]agent.Result // Parse output by job id
	busy     map[string]bool         // jobs whose ResumeJob reports ErrBusy
	classify func(agent.Result) (closeout.Cause, bool)
	limits   LimitRecorder // the Finisher's, kept across rebuild
	readings ReadingRecorder
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{t: t, clk: &clock{t: t0}, gh: fake.New(), ex: &fakeExec{st: map[string]executor.Status{}},
		cm: &fakeCommitter{}, dir: t.TempDir(), results: map[string]agent.Result{}, busy: map[string]bool{}}
	st, err := state.Open(context.Background(), t.TempDir(), state.Options{Now: h.clk.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h.store = st
	h.fx = effects.NewLive(h.gh, gitx.New(""), quiet)
	sec := redact.NewSecrets()
	if err := sec.Add("MODEL_KEY", secretValue); err != nil {
		t.Fatal(err)
	}
	h.closer = closeout.New(closeout.Deps{
		Effects: h.fx, Comments: h.gh, Committer: h.cm, Store: st,
		NewRedactor: func(closeout.Job) (closeout.Redactor, error) { return redact.New(sec, redact.Options{}) },
		Changed: func(context.Context, string, string, ...string) ([]gitx.PathChange, error) {
			return nil, nil
		},
		BaseFile: func(context.Context, string, string, string, int64) ([]byte, bool, error) {
			return nil, false, nil
		},
		Repo: github.Repo{Owner: "o", Name: "r"}, StagingDir: t.TempDir(), Log: quiet, Now: h.clk.now,
	})
	h.cfg = &config.Resolved{Roles: []config.Role{
		{Kind: config.KindTriage, Name: "lead"}, {Kind: config.KindWorker, Name: "engineer"},
		{Kind: config.KindReviewer, Name: "eng-review"}}}
	h.pool = pool.New(pool.Options{Max: 10, Now: h.clk.now})
	h.rebuild()
	return h
}

// busyCloser makes ResumeJob report ErrBusy for chosen jobs.
type busyCloser struct {
	Closer
	busy map[string]bool
}

func (b busyCloser) ResumeJob(ctx context.Context, id string) (closeout.Outcome, error) {
	if b.busy[id] {
		return "", fmt.Errorf("closeout job %s: %w", id, closeout.ErrBusy)
	}
	return b.Closer.ResumeJob(ctx, id)
}

// rebuild creates a Finisher and a Reaper whose start time is now: a process
// restart. Jobs created earlier belong to the previous process.
func (h *harness) rebuild() {
	cl := busyCloser{Closer: h.closer, busy: h.busy}
	h.fin = &Finisher{Closer: cl, Cleaner: h.ex, Store: h.store, Start: h.clk.t, Log: quiet, Limits: h.limits, Readings: h.readings,
		Classify: func(res agent.Result) (closeout.Cause, bool) {
			if h.classify == nil {
				return closeout.Cause{}, false
			}
			return h.classify(res)
		}}
	h.r = New(Deps{
		Store: h.store, Executor: h.ex, Finisher: h.fin, Closer: cl,
		Pool:   func() Lookup { return h.pool },
		Reader: h.gh, Effects: h.fx, Log: quiet, Now: h.clk.now,
		Parse: func(_ context.Context, _ *config.Resolved, sj state.Job, _ executor.Handle, _ int) (agent.Result, error) {
			return h.results[sj.ID], nil
		},
	})
}

// restart simulates a controller restart: time passes, the process restarts and
// the pool is rebuilt from the running jobs, all adopted.
func (h *harness) restart() {
	h.clk.add(time.Hour)
	h.rebuild()
	h.adopt()
}

func (h *harness) adopt() {
	h.t.Helper()
	jobs, err := h.store.RunningJobs(context.Background())
	if err != nil {
		h.t.Fatal(err)
	}
	var held []pool.Held
	for _, j := range jobs {
		held = append(held, pool.Held{Candidate: candidateOf(j), JobID: j.ID})
	}
	h.pool = pool.New(pool.Options{Max: 10, Now: h.clk.now, Held: held})
}

func candidateOf(j state.Job) pool.Candidate {
	class, _ := pool.ParseClass(j.Stage)
	n := j.Issue
	if j.Stage == closeout.StageReview {
		n = j.PR
	}
	return pool.Candidate{Class: class, Role: j.Role, Number: n, Since: j.CreatedAt}
}

// own makes this process the owner of j: a granted slot with its job id set.
func (h *harness) own(j state.Job) *pool.Slot {
	h.t.Helper()
	ds := h.pool.Offer("test", time.Minute, []pool.Candidate{candidateOf(j)})
	if ds[0].Verdict != pool.Granted {
		h.t.Fatalf("grant %s: %v", j.ID, ds[0].Verdict)
	}
	ds[0].Slot.SetJobID(j.ID)
	return ds[0].Slot
}

func (h *harness) seedIssue(n int, stage labels.Stage, role string) {
	h.gh.SeedIssue(github.Issue{Number: n, Title: "t", State: github.StateOpen, Labels: []string{
		labels.Request(stage, role), labels.Running(stage, role)}})
}

type jobOpt func(*jobSpec)

type jobSpec struct {
	stage, role string
	handle      bool
	status      executor.Status
	deadline    time.Duration
}

func noHandle(s *jobSpec)                  { s.handle = false }
func withStatus(st executor.Status) jobOpt { return func(s *jobSpec) { s.status = st } }
func asRole(stage, role string) jobOpt     { return func(s *jobSpec) { s.stage, s.role = stage, role } }
func withDeadline(d time.Duration) jobOpt  { return func(s *jobSpec) { s.deadline = d } }

// startJob claims a worker job on a fresh issue, starts its attempt and stores
// a handle whose log has 60 lines, one of them a known secret.
func (h *harness) startJob(issue int, opts ...jobOpt) (state.Job, executor.Handle) {
	h.t.Helper()
	ctx := context.Background()
	sp := jobSpec{stage: closeout.StageWorker, role: "engineer", handle: true, deadline: 30 * time.Minute,
		status: executor.Status{State: executor.Running, Alive: true, LastOutput: h.clk.t, StartedAt: h.clk.t}}
	for _, o := range opts {
		o(&sp)
	}
	h.seedIssue(issue, labels.Stage(sp.stage), sp.role)
	nj := state.NewJob{Stage: sp.stage, Role: sp.role, Issue: issue, Branch: "worker/" + sp.role + "-x"}
	if sp.stage == closeout.StageReview {
		nj.PR = issue
	}
	sj, err := h.store.CreateJob(ctx, nj)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.store.StartAttempt(ctx, sj.ID); err != nil {
		h.t.Fatal(err)
	}
	dir := filepath.Join(h.dir, sj.ID)
	if err := os.MkdirAll(filepath.Join(dir, "work"), 0o755); err != nil {
		h.t.Fatal(err)
	}
	var b strings.Builder
	for i := 0; i < 60; i++ {
		fmt.Fprintf(&b, "step %02d\n", i)
	}
	b.WriteString("key=" + secretValue + "\n")
	logPath := filepath.Join(dir, sp.stage+"-"+sp.role+"-20260102T030405Z.log")
	if err := os.WriteFile(logPath, []byte(b.String()), 0o600); err != nil {
		h.t.Fatal(err)
	}
	hd := executor.Handle{JobID: sj.ID, Kind: "host", Issue: issue, Stage: sp.stage, Role: sp.role,
		Worktree: filepath.Join(dir, "work"), Home: filepath.Join(dir, "home"), BaseSHA: "abc", LogPath: logPath,
		StdoutPath: filepath.Join(dir, "out.jsonl"), PID: 4242, StartedAt: h.clk.t, Deadline: h.clk.t.Add(sp.deadline)}
	if err := os.WriteFile(hd.StdoutPath, []byte("{}\n"), 0o600); err != nil {
		h.t.Fatal(err)
	}
	if sp.handle {
		raw, _ := json.Marshal(hd)
		if err := h.store.SetHandle(ctx, sj.ID, raw); err != nil {
			h.t.Fatal(err)
		}
		h.ex.st[sj.ID] = sp.status
	}
	sj, _ = h.store.Job(ctx, sj.ID)
	return sj, hd
}

func (h *harness) tick() error {
	return h.r.Tick(context.Background(), controller.Env{Cfg: h.cfg, Log: quiet})
}

func (h *harness) mustTick() {
	h.t.Helper()
	if err := h.tick(); err != nil {
		h.t.Fatalf("tick: %v", err)
	}
}

func (h *harness) job(id string) state.Job {
	h.t.Helper()
	j, err := h.store.Job(context.Background(), id)
	if err != nil {
		h.t.Fatal(err)
	}
	return j
}

func (h *harness) labels(n int) map[string]bool {
	h.t.Helper()
	is, err := h.gh.GetIssue(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	m := map[string]bool{}
	for _, l := range is.Labels {
		m[l] = true
	}
	return m
}

func (h *harness) comments(n int) []github.Comment {
	h.t.Helper()
	cs, err := h.gh.ListComments(context.Background(), n, time.Time{})
	if err != nil {
		h.t.Fatal(err)
	}
	return cs
}

func (h *harness) writes() int { return len(h.gh.Writes()) }

var errBoom = errors.New("boom")

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

func githubIssue(n int, ls ...string) github.Issue {
	return github.Issue{Number: n, Title: "t", State: github.StateOpen, Labels: ls}
}

func githubLabeled(label string, at time.Time) github.TimelineEvent {
	return github.TimelineEvent{Event: "labeled", Label: label, CreatedAt: at}
}

func githubComment(body string) github.Comment { return github.Comment{Author: "ard[bot]", Body: body} }

type spyReader struct {
	Reader
	lists int
}

func (s *spyReader) ListIssues(ctx context.Context, o github.ListIssuesOpts) ([]github.Issue, error) {
	s.lists++
	return s.Reader.ListIssues(ctx, o)
}

type dryEffects struct{ Effects }

func (dryEffects) DryRun() bool { return true }

// claimingReader runs onList after each ListIssues: a claim racing the check.
type claimingReader struct {
	Reader
	onList func()
}

func (c *claimingReader) ListIssues(ctx context.Context, o github.ListIssuesOpts) ([]github.Issue, error) {
	out, err := c.Reader.ListIssues(ctx, o)
	if len(out) > 0 {
		c.onList()
		c.onList = func() {}
	}
	return out, err
}
