package snapshot

import (
	"context"
	"errors"
	"go/build"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/budget"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

var now = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func f64(v float64) *float64 { return &v }

type fakePool struct {
	st   pool.Stats
	held []pool.HeldInfo
}

func (f fakePool) Stats() pool.Stats          { return f.st }
func (f fakePool) HeldSlots() []pool.HeldInfo { return f.held }

type fakeLimits struct {
	rs     []limits.Reading
	blocks map[string]time.Time
}

func (f fakeLimits) Readings() []limits.Reading { return f.rs }
func (f fakeLimits) Until(fam string) time.Time { return f.blocks[fam] }

type fakeState struct {
	running, ended []state.Job
	job            map[string]state.JobUsage
	issue          map[int]map[string]state.JobUsage
	per            map[state.RoleKey]state.Spend
	repo           state.Spend
	err            error
	issueCalls     atomic.Int32
	since          time.Time
}

func (f *fakeState) RunningJobs(context.Context) ([]state.Job, error) { return f.running, nil }
func (f *fakeState) EndedSince(_ context.Context, since time.Time) ([]state.Job, error) {
	f.since = since
	var out []state.Job
	for _, j := range f.ended {
		if !j.EndedAt.Before(since) {
			out = append(out, j)
		}
	}
	return out, nil
}
func (f *fakeState) JobUsage(_ context.Context, id string) (state.JobUsage, error) {
	return f.job[id], nil
}
func (f *fakeState) IssueUsage(_ context.Context, n int) (map[string]state.JobUsage, error) {
	f.issueCalls.Add(1)
	if f.err != nil {
		return nil, f.err
	}
	return f.issue[n], nil
}
func (f *fakeState) SpendSince(context.Context, time.Time) (map[state.RoleKey]state.Spend, state.Spend, error) {
	return f.per, f.repo, nil
}

type fakeControl struct {
	p map[string]controller.PauseState
	t []controller.TickInfo
}

func (f fakeControl) PauseStates() map[string]controller.PauseState { return f.p }
func (f fakeControl) Ticks() []controller.TickInfo                  { return f.t }

type fakeCaps struct{ caps []budget.Cap }

func (f fakeCaps) Capped(context.Context) ([]budget.Cap, time.Time, error) { return f.caps, now, nil }

func cfg() *config.Resolved {
	return &config.Resolved{BudgetUSDPerDay: 50, Roles: []config.Role{
		{Kind: config.KindWorker, Name: "engineer", Model: "claude-opus-5", Timeout: 30 * time.Minute},
		{Kind: config.KindTriage, Name: "lead", Model: "sonnet", Timeout: 10 * time.Minute},
	}}
}

func taker(s Sources) *Taker {
	s.Now = func() time.Time { return now }
	return New(s)
}

func TestPoolView(t *testing.T) {
	st := pool.Stats{
		Max: 3, Held: 2,
		WaitingBy: map[pool.Class]int{pool.ClassTriage: 2},
		WaitingRoles: []pool.RoleWait{
			{Class: pool.ClassTriage, Role: "lead", Count: 2, Oldest: time.Minute},
		},
		RefusedBy: map[pool.Class][]pool.Refusal{
			pool.ClassWorker: {
				{Role: "engineer", Reason: pool.DeniedBudget, Count: 1},
				{Role: "engineer", Reason: pool.DeniedLimited, Count: 3},
			},
		},
	}
	held := []pool.HeldInfo{{Key: "a"}, {Key: "b"}}
	snap := taker(Sources{Pool: func() PoolSource { return fakePool{st, held} }}).Take(context.Background())
	if snap.Pool.Used != 2 || snap.Pool.Max != 3 {
		t.Errorf("pool = %+v", snap.Pool)
	}
	want := []Waiting{
		{Class: "triage", Role: "lead", Reason: ReasonSlot, Count: 2, Oldest: time.Minute},
		{Class: "worker", Role: "engineer", Reason: ReasonBudget, Count: 1},
		{Class: "worker", Role: "engineer", Reason: ReasonLimited, Count: 3},
	}
	if len(snap.Pool.Waiting) != len(want) {
		t.Fatalf("waiting = %+v", snap.Pool.Waiting)
	}
	for i, w := range want {
		if snap.Pool.Waiting[i] != w {
			t.Errorf("waiting[%d] = %+v, want %+v", i, snap.Pool.Waiting[i], w)
		}
	}
}

func TestRunningJobs(t *testing.T) {
	fs := &fakeState{
		running: []state.Job{
			{ID: "J1", Stage: "worker", Role: "engineer", Issue: 7, PR: 9, CreatedAt: now.Add(-5 * time.Minute)},
			{ID: "J2", Stage: "triage", Role: "lead", Issue: 8, CreatedAt: now.Add(-time.Minute)},
			{ID: "J3", Stage: "worker", Role: "gone", Issue: 9, CreatedAt: now},
		},
		// J1: a finished step cost 0.40; its running step has no row yet.
		job: map[string]state.JobUsage{"J1": {CostUSD: f64(0.40), Steps: 1}},
	}
	snap := taker(Sources{State: fs, Config: cfg}).Take(context.Background())
	if len(snap.Running) != 3 {
		t.Fatalf("running = %+v", snap.Running)
	}
	j1, j2, j3 := snap.Running[0], snap.Running[1], snap.Running[2]
	if j1.Issue != 7 || j1.PR != 9 || j1.Stage != "worker" || j1.Role != "engineer" || j1.Model != "claude-opus-5" ||
		j1.Elapsed != 5*time.Minute || j1.Timeout != 30*time.Minute {
		t.Errorf("j1 = %+v", j1)
	}
	if j1.FinishedCostUSD == nil || *j1.FinishedCostUSD != 0.40 {
		t.Errorf("j1 cost = %v, want finished steps only (0.40)", j1.FinishedCostUSD)
	}
	if j2.FinishedCostUSD != nil || j2.Model != "sonnet" || j2.Timeout != 10*time.Minute {
		t.Errorf("j2 = %+v (no steps => nil cost)", j2)
	}
	if j3.Model != "" || j3.Timeout != 0 {
		t.Errorf("j3 with no role config = %+v", j3)
	}
	// Nil config still fills the rest.
	if s := taker(Sources{State: fs}).Take(context.Background()); len(s.Running) != 3 || s.Running[0].Timeout != 0 {
		t.Errorf("nil config: %+v", s.Running)
	}
}

func TestPauseAndTicks(t *testing.T) {
	start, end := now.Add(-time.Second), now
	ctl := fakeControl{
		p: map[string]controller.PauseState{
			"worker":   {Paused: true, Reason: controller.ReasonLabel},
			"triage":   {Paused: true, Reason: controller.ReasonUnreadable, Err: errors.New("boom")},
			"reviewer": {Paused: true, Reason: controller.ReasonPauseFile},
			"merger":   {Paused: true, Reason: controller.ReasonConfig},
			"scanner":  {},
		},
		t: []controller.TickInfo{{Loop: "worker", Count: 4, Start: start, End: end, Err: "x"}},
	}
	snap := taker(Sources{Control: ctl}).Take(context.Background())
	var got []string
	for _, p := range snap.Pause {
		got = append(got, p.Loop+":"+string(p.Reason)+":"+p.Err)
	}
	want := "merger:config:,reviewer:pause-file:,triage:status-unreadable:boom,worker:label:"
	if strings.Join(got, ",") != want {
		t.Errorf("pause = %v", got)
	}
	if len(snap.Ticks) != 1 || snap.Ticks[0] != (TickView{Loop: "worker", Count: 4, Start: start, End: end, Err: "x"}) {
		t.Errorf("ticks = %+v", snap.Ticks)
	}
}

func TestLimits(t *testing.T) {
	obs := now.Add(-90 * time.Second)
	reset := now.Add(2 * time.Hour)
	fl := fakeLimits{
		rs: []limits.Reading{
			{Window: "five_hour", Utilization: 0.42, Status: "allowed", ResetsAt: reset, ObservedAt: obs},
			{Window: "seven_day", Utilization: 85, Status: "allowed_warning", ResetsAt: reset, ObservedAt: now},
		},
		blocks: map[string]time.Time{"opus": now.Add(time.Hour), "haiku": now.Add(-time.Hour)},
	}
	snap := taker(Sources{Limits: func() LimitSource { return fl }}).Take(context.Background())
	w := snap.Limits.Windows
	if len(w) != 2 || w[0].Utilization != 0.42 || w[0].Status != "allowed" || !w[0].ResetsAt.Equal(reset) || w[0].Age != 90*time.Second {
		t.Errorf("windows = %+v", w)
	}
	if w[1].Utilization != 0.85 {
		t.Errorf("percent stream not normalised: %v", w[1].Utilization)
	}
	if b := snap.Limits.Blocks; len(b) != 1 || b[0].Family != "opus" || !b[0].Until.Equal(now.Add(time.Hour)) {
		t.Errorf("blocks = %+v (expired haiku block must not show)", b)
	}
	// Empty book.
	empty := taker(Sources{Limits: func() LimitSource { return fakeLimits{} }}).Take(context.Background())
	if len(empty.Limits.Windows) != 0 || len(empty.Limits.Blocks) != 0 {
		t.Errorf("empty = %+v", empty.Limits)
	}
}

func TestNormalizeUtilization(t *testing.T) {
	for in, want := range map[float64]float64{0: 0, 0.5: 0.5, 1: 1, 50: 0.5, 100: 1, 250: 1, -3: 0} {
		if got := NormalizeUtilization(in); got != want {
			t.Errorf("NormalizeUtilization(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestIssuesAndCache(t *testing.T) {
	dayStart := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	fs := &fakeState{
		running: []state.Job{{ID: "R1", Stage: "worker", Role: "engineer", Issue: 3, Status: state.StatusRunning, UpdatedAt: now}},
		ended: []state.Job{
			{ID: "E1", Stage: "triage", Role: "lead", Issue: 5, Status: state.StatusDone, EndedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)},
			{ID: "E2", Stage: "triage", Role: "lead", Issue: 6, Status: state.StatusDone, EndedAt: dayStart.Add(-time.Minute), UpdatedAt: dayStart},
			{ID: "S1", Stage: "scanner", Role: "cve", Issue: 0, Status: state.StatusDone, EndedAt: now},
		},
		issue: map[int]map[string]state.JobUsage{
			5: {"E1": {CostUSD: f64(0.25)}, "E0": {CostUSD: f64(0.5)}},
			3: {"R1": {CostUSD: f64(1)}},
		},
	}
	tk := taker(Sources{State: fs})
	snap := tk.Take(context.Background())
	if !fs.since.Equal(dayStart) || !snap.DayFrom.Equal(dayStart) {
		t.Errorf("since = %v", fs.since)
	}
	if len(snap.Issues) != 2 || snap.Issues[0].Issue != 3 || snap.Issues[1].Issue != 5 {
		t.Fatalf("issues = %+v (a job from 23:59 yesterday and scanner runs are excluded)", snap.Issues)
	}
	if i := snap.Issues[1]; i.CostUSD == nil || *i.CostUSD != 0.75 || i.State != "triage:lead done" || i.Jobs != 1 {
		t.Errorf("issue 5 = %+v", i)
	}
	if i := snap.Issues[0]; i.State != "worker:engineer running" || i.CostUSD == nil || *i.CostUSD != 1 {
		t.Errorf("issue 3 = %+v", i)
	}
	// Finished issue is cached; the running one is re-read each time.
	if n := fs.issueCalls.Load(); n != 2 {
		t.Fatalf("first take: IssueUsage calls = %d, want 2", n)
	}
	tk.Take(context.Background())
	if n := fs.issueCalls.Load(); n != 3 {
		t.Errorf("second take: IssueUsage calls = %d, want 3 (only the running issue)", n)
	}
	tk.Invalidate(5)
	tk.Take(context.Background())
	if n := fs.issueCalls.Load(); n != 5 {
		t.Errorf("after Invalidate: IssueUsage calls = %d, want 5", n)
	}
	// A changed job invalidates the cache on its own.
	fs.ended[0].UpdatedAt = now
	tk.Take(context.Background())
	if n := fs.issueCalls.Load(); n != 7 {
		t.Errorf("after job update: IssueUsage calls = %d, want 7", n)
	}
}

func TestSpend(t *testing.T) {
	fs := &fakeState{
		per: map[state.RoleKey]state.Spend{
			{Stage: "worker", Role: "engineer"}: {USD: 12.5},
			{Stage: "triage", Role: "lead"}:     {USD: 1.5},
		},
		repo: state.Spend{USD: 14, Unpriced: 2},
	}
	capped := []budget.Cap{{Scope: "role", Stage: "worker", Role: "engineer", Limit: 10, Spent: state.Spend{USD: 12.5}}}
	snap := taker(Sources{State: fs, Config: cfg, Caps: fakeCaps{capped}}).Take(context.Background())
	sp := snap.Spend
	if sp.RepoUSD != 14 || sp.RepoCap != 50 || sp.Unpriced != 2 || len(sp.Roles) != 2 || sp.Roles[0].Role != "lead" ||
		sp.Roles[1].USD != 12.5 || len(sp.CappedNow) != 1 {
		t.Errorf("spend = %+v", sp)
	}
	if s := taker(Sources{State: fs}).Take(context.Background()); s.Spend.RepoCap != 0 || s.Spend.RepoUSD != 14 {
		t.Errorf("nil config spend = %+v", s.Spend)
	}
}

func TestSourceErrorKeepsOtherFields(t *testing.T) {
	fs := &fakeState{
		ended: []state.Job{{ID: "E1", Stage: "triage", Role: "lead", Issue: 5, Status: state.StatusDone, EndedAt: now}},
		repo:  state.Spend{USD: 3},
		err:   errors.New("disk"),
	}
	st := pool.Stats{Max: 2}
	snap := taker(Sources{State: fs, Pool: func() PoolSource { return fakePool{st: st} }}).Take(context.Background())
	if snap.Pool.Max != 2 || snap.Spend.RepoUSD != 3 || len(snap.Issues) != 1 {
		t.Errorf("snapshot = %+v", snap)
	}
	if len(snap.Errors) != 1 || !strings.Contains(snap.Errors[0], "disk") {
		t.Errorf("errors = %v", snap.Errors)
	}
}

func TestEmptySources(t *testing.T) {
	snap := New(Sources{}).Take(context.Background())
	if snap.TakenAt.IsZero() || snap.Pool.Max != 0 || len(snap.Errors) != 0 {
		t.Errorf("snapshot = %+v", snap)
	}
	snap = taker(Sources{Pool: func() PoolSource { return nil }, Limits: func() LimitSource { return nil }}).Take(context.Background())
	if len(snap.Errors) != 0 {
		t.Errorf("nil sources: %+v", snap)
	}
}

func TestEventsIncluded(t *testing.T) {
	ring := NewRing(4)
	ring.Append(Event{Msg: "a"})
	snap := taker(Sources{Ring: ring}).Take(context.Background())
	if len(snap.Events) != 1 || snap.Events[0].Msg != "a" {
		t.Errorf("events = %+v", snap.Events)
	}
}

// countingGetter is the PauseGate's one GitHub read, counted.
type countingGetter struct {
	f *fake.Fake
	n atomic.Int32
}

func (c *countingGetter) GetIssue(ctx context.Context, n int) (github.Issue, error) {
	c.n.Add(1)
	return c.f.GetIssue(ctx, n)
}

type loop struct{}

func (loop) Name() string                               { return "claiming" }
func (loop) Claims() bool                               { return true }
func (loop) Interval(*config.Resolved) time.Duration    { return time.Hour }
func (loop) Tick(context.Context, controller.Env) error { return nil }

func TestTakeMakesNoGitHubCall(t *testing.T) {
	f := fake.New()
	n := f.SeedIssue(github.Issue{})
	cg := &countingGetter{f: f}
	gate := controller.NewPauseGate(cg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	gate.SetStatusIssue(n)
	c, err := controller.New(controller.Deps{
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: func(context.Context) (*config.Resolved, error) { return cfg(), nil },
		Pause:  gate,
	}, loop{})
	if err != nil {
		t.Fatal(err)
	}
	tk := taker(Sources{Control: c, Config: cfg, State: &fakeState{}, Pool: func() PoolSource { return fakePool{} }})
	for range 3 {
		tk.Take(context.Background())
	}
	if got := cg.n.Load(); got != 0 {
		t.Errorf("GetIssue calls = %d, want 0", got)
	}
	if w := f.Writes(); len(w) != 0 {
		t.Errorf("writes = %v", w)
	}
}

func TestNoGitHubImport(t *testing.T) {
	pkg, err := build.ImportDir(".", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, imp := range pkg.Imports {
		if strings.HasSuffix(imp, "/internal/github") || strings.Contains(imp, "/internal/github/") {
			t.Errorf("snapshot imports %s", imp)
		}
	}
}
