package scanner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/redact"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

func tctx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// fakeEngine returns the scripted findings of the next scan.
type fakeEngine struct {
	mu       sync.Mutex
	calls    int
	findings []Finding
	sha      string
	errs     []error // popped one per call before findings are returned
}

func (e *fakeEngine) set(fs ...Finding) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.findings = fs
}

func (e *fakeEngine) failNext(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errs = append(e.errs, err)
}

func (e *fakeEngine) Scan(_ context.Context, _ *config.Resolved, _ config.Role, _ time.Time) (ScanResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if len(e.errs) > 0 {
		err := e.errs[0]
		e.errs = e.errs[1:]
		return ScanResult{SHA: e.sha}, err
	}
	return ScanResult{SHA: e.sha, Findings: append([]Finding(nil), e.findings...)}, nil
}

// fakeCommitter records requests.
type fakeCommitter struct {
	mu   sync.Mutex
	reqs []committer.Request
	err  error
}

func (c *fakeCommitter) Commit(_ context.Context, req committer.Request) (committer.Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return committer.Result{}, c.err
	}
	c.reqs = append(c.reqs, req)
	return committer.Result{SHA: "c0ffee"}, nil
}

func (c *fakeCommitter) paths() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, r := range c.reqs {
		for _, f := range r.Files {
			out = append(out, f.Path)
		}
	}
	return out
}

// flakyStore fails the next n Apply calls.
type flakyStore struct {
	Store
	failApply int
}

func (s *flakyStore) Apply(ctx context.Context, b scannerdb.Batch) error {
	if s.failApply > 0 {
		s.failApply--
		return errors.New("disk full")
	}
	return s.Store.Apply(ctx, b)
}

type harness struct {
	t     *testing.T
	ctx   context.Context
	gh    *fake.Fake
	fx    effects.Effects
	eng   *fakeEngine
	com   *fakeCommitter
	db    *scannerdb.DB
	store *flakyStore
	p     *Pipeline
	cfg   *config.Resolved
	role  config.Role
	clock time.Time
}

func scannerRole(name string) config.Role {
	return config.Role{
		Kind: config.KindScanner, Name: name, Dir: ".ard/scanners/" + name, ScanPath: ".ard/scanners/" + name + "/scan",
		Timeout: 30 * time.Minute, Every: 6 * time.Hour, MaxOpen: 5, GroupSize: 10, Autoclose: true, Route: config.RoutePending,
	}
}

// newHarness wires a Pipeline to the GitHub fake through the live effect
// layer, so a second run sees what the first one filed.
func newHarness(t *testing.T, mod func(*config.Role)) *harness {
	t.Helper()
	h := &harness{t: t, ctx: tctx(t), clock: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC), eng: &fakeEngine{sha: "sha-1"}, com: &fakeCommitter{}}
	h.gh = fake.New(fake.WithClock(func() time.Time { return h.clock }))
	h.fx = effects.NewLive(h.gh, nil, quiet)
	h.role = scannerRole("cve")
	if mod != nil {
		mod(&h.role)
	}
	h.cfg = &config.Resolved{GlobalMaxOpen: 30, Stale: config.Stale{SuppressFor: config.Duration(90 * 24 * time.Hour)}, Roles: []config.Role{h.role}}
	db, err := scannerdb.Open(h.ctx, t.TempDir(), h.role.Name, scannerdb.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	h.db, h.store = db, &flakyStore{Store: db}
	h.p = &Pipeline{
		Reader: h.gh, Effects: h.fx, Committer: h.com, Engine: h.eng, Repo: "",
		Stores: func(context.Context, string) (Store, error) { return h.store, nil },
		Log:    quiet, Now: func() time.Time { return h.clock },
		Secrets: redact.NewSecrets,
	}
	return h
}

// run advances the clock an hour and runs the scanner once.
func (h *harness) run() (RunResult, error) {
	h.t.Helper()
	h.clock = h.clock.Add(time.Hour)
	return h.p.Run(h.ctx, h.cfg, h.role, h.clock)
}

func (h *harness) mustRun() RunResult {
	h.t.Helper()
	res, err := h.run()
	if err != nil {
		h.t.Fatalf("run: %v", err)
	}
	return res
}

func (h *harness) issue(n int) github.Issue {
	h.t.Helper()
	is, err := h.gh.GetIssue(h.ctx, n)
	if err != nil {
		h.t.Fatal(err)
	}
	return is
}

func (h *harness) all() []github.Issue {
	h.t.Helper()
	is, err := h.gh.ListIssues(h.ctx, github.ListIssuesOpts{State: github.StateAll})
	if err != nil {
		h.t.Fatal(err)
	}
	return is
}

func (h *harness) openIssues() []github.Issue {
	h.t.Helper()
	is, err := h.gh.ListIssues(h.ctx, github.ListIssuesOpts{State: github.StateOpen})
	if err != nil {
		h.t.Fatal(err)
	}
	return is
}

func (h *harness) close(n int, reason github.StateReason) {
	h.t.Helper()
	st := github.StateClosed
	if err := h.gh.As("human").UpdateIssue(h.ctx, n, github.IssueUpdate{State: &st, StateReason: &reason}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) reopen(n int) {
	h.t.Helper()
	st := github.StateOpen
	if err := h.gh.As("human").UpdateIssue(h.ctx, n, github.IssueUpdate{State: &st}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) label(n int, lbls ...string) {
	h.t.Helper()
	if err := h.gh.As("human").AddLabels(h.ctx, n, lbls...); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) row(fp string) (Row, bool) {
	h.t.Helper()
	r, err := h.db.Finding(h.ctx, fp)
	if errors.Is(err, scannerdb.ErrNotFound) {
		return Row{}, false
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return r, true
}

func (h *harness) writes(method string) int {
	n := 0
	for _, w := range h.gh.Writes() {
		if w.Method == method {
			n++
		}
	}
	return n
}

func (h *harness) lastRun() scannerdb.Run {
	h.t.Helper()
	r, err := h.db.LastRun(h.ctx)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

func (h *harness) comments(n int) []string {
	h.t.Helper()
	cs, err := h.gh.ListComments(h.ctx, n, time.Time{})
	if err != nil {
		h.t.Fatal(err)
	}
	var out []string
	for _, c := range cs {
		out = append(out, c.Body)
	}
	return out
}

func fnd(fp string, sev Severity) Finding {
	return Finding{Fingerprint: fp, Title: "title " + fp, Severity: sev}
}

func gfnd(fp string, sev Severity, group string) Finding {
	f := fnd(fp, sev)
	f.Group = group
	return f
}

func (h *harness) wantRow(fp string, issue int, st scannerdb.Status) {
	h.t.Helper()
	r, ok := h.row(fp)
	if !ok || r.Issue != issue || r.Status != st {
		h.t.Fatalf("row %s = %+v (found %v), want issue %d status %s", fp, r, ok, issue, st)
	}
}

func contains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Errorf("missing %q in\n%s", want, got)
	}
}

var _ = fmt.Sprint
