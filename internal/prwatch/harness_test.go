package prwatch

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

const self = "ard[bot]"

var (
	quiet = slog.New(slog.NewTextHandler(io.Discard, nil))
	t0    = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
)

// counting wraps the fake and counts the reads the cost rule is about.
type counting struct {
	*fake.Fake
	mu    sync.Mutex
	calls map[string]int
}

func (c *counting) n(m string) {
	c.mu.Lock()
	c.calls[m]++
	c.mu.Unlock()
}

func (c *counting) get(m string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[m]
}

func (c *counting) reset() {
	c.mu.Lock()
	c.calls = map[string]int{}
	c.mu.Unlock()
}

func (c *counting) ListPRs(ctx context.Context, o github.ListPRsOpts) ([]github.PullRequest, error) {
	c.n("ListPRs")
	return c.Fake.ListPRs(ctx, o)
}

func (c *counting) RequiredChecks(ctx context.Context, b string) (github.RequiredChecksResult, error) {
	c.n("RequiredChecks")
	return c.Fake.RequiredChecks(ctx, b)
}

func (c *counting) ListCheckRuns(ctx context.Context, sha string) ([]github.CheckRun, error) {
	c.n("ListCheckRuns")
	return c.Fake.ListCheckRuns(ctx, sha)
}

func (c *counting) ListCommitStatuses(ctx context.Context, sha string) ([]github.CommitStatus, error) {
	c.n("ListCommitStatuses")
	return c.Fake.ListCommitStatuses(ctx, sha)
}

type harness struct {
	t      *testing.T
	gh     *fake.Fake
	rd     *counting
	fx     effects.Effects
	store  *state.Store
	loop   *Loop
	now    time.Time
	status int
	cfg    *config.Resolved
	ledger string
}

func newHarness(t *testing.T, dry bool) *harness {
	t.Helper()
	gh := fake.New(fake.WithIdentity(github.Identity{Login: self, ID: 7, Kind: github.AuthApp}))
	h := &harness{t: t, gh: gh, rd: &counting{Fake: gh, calls: map[string]int{}}, now: t0,
		cfg: &config.Resolved{SHA: "abc", PRWatch: config.PRWatch{PendingTimeout: config.Duration(2 * time.Hour)}}}
	st, err := state.Open(context.Background(), t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h.store = st
	if dry {
		h.ledger = filepath.Join(t.TempDir(), "effects.jsonl")
		d, err := effects.NewDryRun(effects.DryRunOptions{Log: quiet, LedgerPath: h.ledger})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { d.Close() })
		h.fx = d
	} else {
		h.fx = effects.NewLive(gh, gitx.New(""), quiet)
	}
	h.status = gh.SeedIssue(github.Issue{Title: "ard status", Author: self, State: github.StateOpen})
	h.restart()
	return h
}

// restart replaces the loop with a fresh one over the same store.
func (h *harness) restart() {
	h.loop = New(Deps{Reader: h.rd, Effects: h.fx, Store: h.store, StatusIssue: func() int { return h.status },
		Now: func() time.Time { return h.now }, Log: quiet})
}

func (h *harness) tick() error {
	h.t.Helper()
	return h.loop.Tick(context.Background(), controller.Env{Cfg: h.cfg, Log: quiet})
}

func (h *harness) mustTick() {
	h.t.Helper()
	if err := h.tick(); err != nil {
		h.t.Fatalf("tick: %v", err)
	}
}

type prOpt func(*github.PullRequest)

func draft(p *github.PullRequest)     { p.Draft = true }
func foreign(p *github.PullRequest)   { p.Author = "someone" }
func badBranch(p *github.PullRequest) { p.HeadRef = "feature/x" }

// pr seeds an open issue and its worker PR with head sha; it returns both numbers.
func (h *harness) pr(sha string, opts ...prOpt) (pr, issue int) {
	h.t.Helper()
	issue = h.gh.SeedIssue(github.Issue{Title: "Fix", Author: "human", State: github.StateOpen,
		Labels: []string{labels.Done(labels.StageWorker, "eng")}})
	p := github.PullRequest{Title: "Fix", Author: self, State: github.StateOpen, HeadRef: "worker/eng-" + itoa(issue) + "-fix",
		HeadSHA: sha, BaseRef: "main"}
	for _, o := range opts {
		o(&p)
	}
	return h.gh.SeedPR(p), issue
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func (h *harness) close(n int) {
	h.t.Helper()
	st := github.StateClosed
	if err := h.gh.UpdateIssue(context.Background(), n, github.IssueUpdate{State: &st}); err != nil {
		h.t.Fatal(err)
	}
}

// comments returns the bodies on n that carry the prwatch marker.
func (h *harness) comments(n int) []string {
	h.t.Helper()
	cs, err := h.gh.ListComments(context.Background(), n, time.Time{})
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		if strings.Contains(c.Body, "<!-- ard:prwatch") {
			out = append(out, c.Body)
		}
	}
	return out
}

func (h *harness) issueLabels(n int) []string {
	h.t.Helper()
	is, err := h.gh.GetIssue(context.Background(), n)
	if err != nil {
		h.t.Fatal(err)
	}
	return is.Labels
}

func (h *harness) events(kind string) []state.Event {
	h.t.Helper()
	evs, err := h.store.Events(context.Background(), state.EventFilter{Kind: kind})
	if err != nil {
		h.t.Fatal(err)
	}
	return evs
}

func has(ls []string, l string) bool {
	for _, x := range ls {
		if x == l {
			return true
		}
	}
	return false
}
