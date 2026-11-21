package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github/fake"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

type namedEngine struct {
	ran    []string
	tried  []string
	noSlot bool
}

func (e *namedEngine) Scan(_ context.Context, _ *config.Resolved, r config.Role, _ time.Time) (ScanResult, error) {
	e.tried = append(e.tried, r.Name)
	if e.noSlot {
		return ScanResult{}, ErrNoSlot
	}
	e.ran = append(e.ran, r.Name)
	return ScanResult{SHA: "s"}, nil
}

type fakeBuiltin struct {
	name  string
	every time.Duration
	err   error
	runs  int
}

func (b *fakeBuiltin) Name() string         { return b.name }
func (b *fakeBuiltin) Every() time.Duration { return b.every }
func (b *fakeBuiltin) Run(context.Context) error {
	b.runs++
	return b.err
}

type loopFixture struct {
	t     *testing.T
	ctx   context.Context
	loop  *Loop
	eng   *namedEngine
	reg   *scannerdb.Registry
	cfg   *config.Resolved
	clock time.Time
	env   controller.Env
}

func newLoopFixture(t *testing.T, roles []config.Role, builtins ...Builtin) *loopFixture {
	t.Helper()
	f := &loopFixture{t: t, ctx: tctx(t), eng: &namedEngine{}, clock: time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)}
	f.reg = scannerdb.NewRegistry(t.TempDir(), scannerdb.Options{})
	t.Cleanup(func() { f.reg.Close() })
	gh := fake.New()
	f.cfg = &config.Resolved{GlobalMaxOpen: 30, Roles: roles, Stale: config.Stale{SuppressFor: config.Duration(24 * time.Hour)}}
	p := &Pipeline{
		Reader: gh, Effects: effects.NewLive(gh, nil, quiet), Committer: &fakeCommitter{}, Engine: f.eng,
		Stores: RegistryStores(f.reg), Log: quiet, Now: func() time.Time { return f.clock },
	}
	f.loop = NewLoop(p, builtins...)
	f.env = controller.Env{Log: quiet, Cfg: f.cfg}
	return f
}

func (f *loopFixture) lastRun(name string) (scannerdb.Run, bool) {
	f.t.Helper()
	db, err := f.reg.Get(f.ctx, name)
	if err != nil {
		f.t.Fatal(err)
	}
	r, err := db.LastRun(f.ctx)
	if errors.Is(err, scannerdb.ErrNotFound) {
		return scannerdb.Run{}, false
	}
	if err != nil {
		f.t.Fatal(err)
	}
	return r, true
}

func (f *loopFixture) seedRun(name string, ago time.Duration) {
	f.t.Helper()
	db, _ := f.reg.Get(f.ctx, name)
	if _, err := db.RecordRun(f.ctx, scannerdb.Run{StartedAt: f.clock.Add(-ago), Outcome: "ok"}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *loopFixture) tick() {
	f.t.Helper()
	if err := f.loop.Tick(f.ctx, f.env); err != nil {
		f.t.Fatal(err)
	}
}

func every(name string, d time.Duration) config.Role {
	r := scannerRole(name)
	r.Every = d
	return r
}

func TestLoopContract(t *testing.T) {
	f := newLoopFixture(t, []config.Role{every("cve", time.Hour)})
	if f.loop.Name() != "scanners" || !f.loop.Claims() || f.loop.Interval(f.cfg) != 5*time.Minute {
		t.Errorf("name %q claims %v interval %v", f.loop.Name(), f.loop.Claims(), f.loop.Interval(f.cfg))
	}
}

func TestLoopRunsOneMostOverdueScannerPerTick(t *testing.T) {
	f := newLoopFixture(t, []config.Role{every("cve", 6*time.Hour), every("lint", 6*time.Hour), every("docs", 6*time.Hour)})
	f.seedRun("cve", 10*time.Hour)  // due 4h ago
	f.seedRun("lint", 20*time.Hour) // due 14h ago
	f.seedRun("docs", time.Hour)    // not due
	f.tick()
	if len(f.eng.ran) != 1 || f.eng.ran[0] != "lint" {
		t.Fatalf("ran %v, want only the most overdue (lint)", f.eng.ran)
	}
	f.clock = f.clock.Add(time.Minute)
	f.tick()
	if len(f.eng.ran) != 2 || f.eng.ran[1] != "cve" {
		t.Fatalf("ran %v", f.eng.ran)
	}
	f.clock = f.clock.Add(time.Minute)
	f.tick()
	if len(f.eng.ran) != 2 {
		t.Errorf("a scanner that is not due ran: %v", f.eng.ran)
	}
}

func TestLoopNewScannerRunsImmediately(t *testing.T) {
	f := newLoopFixture(t, []config.Role{every("cve", 6*time.Hour)})
	f.tick()
	if len(f.eng.ran) != 1 {
		t.Fatalf("ran %v", f.eng.ran)
	}
	if r, ok := f.lastRun("cve"); !ok || r.Outcome != OutcomeOK {
		t.Errorf("run = %+v", r)
	}
}

func TestLoopDeniedRunStaysDue(t *testing.T) {
	f := newLoopFixture(t, []config.Role{every("cve", 6*time.Hour)})
	f.seedRun("cve", 7*time.Hour)
	f.eng.noSlot = true
	f.tick()
	r, _ := f.lastRun("cve")
	if want := f.clock.Add(-7 * time.Hour); !r.StartedAt.Equal(want) {
		t.Fatalf("a denied run was recorded: %+v", r)
	}
	// Offered again on the next tick, not dropped until the next interval.
	f.clock = f.clock.Add(5 * time.Minute)
	f.eng.noSlot = false
	f.tick()
	if len(f.eng.ran) != 1 {
		t.Errorf("ran %v", f.eng.ran)
	}
}

func TestLoopDroppedRunCountsAsARun(t *testing.T) {
	f := newLoopFixture(t, []config.Role{every("cve", 6*time.Hour)})
	f.cfg.GlobalMaxOpen = 0 // everything is dropped by the global cap
	f.seedRun("cve", 7*time.Hour)
	f.tick()
	r, _ := f.lastRun("cve")
	if r.Outcome != OutcomeGlobalCap {
		t.Fatalf("run = %+v", r)
	}
	f.clock = f.clock.Add(5 * time.Minute)
	f.tick() // not due again for 6h: the next scheduled run checks again
	if r2, _ := f.lastRun("cve"); r2.ID != r.ID {
		t.Errorf("a dropped run was retried before its next interval")
	}
}

func TestLoopSkipsLLMScannersAndBuiltinCollisions(t *testing.T) {
	llm := every("arch", time.Hour)
	llm.ScanPath, llm.PromptPath = "", ".ard/scanners/arch/prompt.txt"
	b := &fakeBuiltin{name: "stale", every: time.Hour}
	f := newLoopFixture(t, []config.Role{llm, every("stale", time.Hour)}, b)
	f.tick()
	if len(f.eng.ran) != 0 {
		t.Errorf("ran %v", f.eng.ran)
	}
	if b.runs != 1 {
		t.Errorf("the built-in did not run: %d", b.runs)
	}
}

func TestLoopRunsBuiltinsFromTheirOwnDatabase(t *testing.T) {
	b := &fakeBuiltin{name: "stale", every: 24 * time.Hour}
	f := newLoopFixture(t, nil, b)
	f.tick()
	if b.runs != 1 {
		t.Fatalf("runs = %d", b.runs)
	}
	if r, ok := f.lastRun("stale"); !ok || r.Outcome != OutcomeOK {
		t.Errorf("run = %+v", r)
	}
	f.clock = f.clock.Add(time.Hour)
	f.tick()
	if b.runs != 1 {
		t.Errorf("ran again before its interval: %d", b.runs)
	}
	f.clock = f.clock.Add(24 * time.Hour)
	b.err = errors.New("boom")
	if err := f.loop.Tick(f.ctx, f.env); err == nil {
		t.Error("a failing built-in did not surface")
	}
	if r, _ := f.lastRun("stale"); r.Outcome != OutcomeError {
		t.Errorf("run = %+v", r)
	}
}

func TestLoopTickTimeout(t *testing.T) {
	a := every("a", time.Hour)
	a.Timeout = 10 * time.Minute
	b := every("b", time.Hour)
	b.Timeout = 45 * time.Minute
	llm := every("c", time.Hour)
	llm.ScanPath = ""
	llm.Timeout = 5 * time.Hour
	f := newLoopFixture(t, []config.Role{a, b, llm})
	if got := f.loop.TickTimeout(f.cfg); got != 45*time.Minute+tickSlack {
		t.Errorf("TickTimeout = %v", got)
	}
	if got := newLoopFixture(t, nil).loop.TickTimeout(&config.Resolved{}); got != 0 {
		t.Errorf("no scanners: %v, want the default", got)
	}
}

func TestLoopDeniedScannerDoesNotStarveOthers(t *testing.T) {
	b := &fakeBuiltin{name: "stale", every: time.Hour}
	f := newLoopFixture(t, []config.Role{every("cve", 6*time.Hour), every("lint", 6*time.Hour)}, b)
	f.seedRun("cve", 20*time.Hour)
	f.seedRun("lint", 10*time.Hour)
	f.seedRun("stale", 5*time.Hour)
	f.eng.noSlot = true
	f.tick()
	if len(f.eng.tried) != 2 || f.eng.tried[0] != "cve" || f.eng.tried[1] != "lint" {
		t.Errorf("tried %v: every due scanner must be offered, oldest first", f.eng.tried)
	}
	if b.runs != 1 {
		t.Errorf("the built-in was starved by denied scanners: %d runs", b.runs)
	}
	// Once one scanner does run, the walk stops.
	f.eng.noSlot = false
	f.eng.tried, f.eng.ran = nil, nil
	f.clock = f.clock.Add(time.Minute)
	f.tick()
	if len(f.eng.ran) != 1 || f.eng.ran[0] != "cve" {
		t.Errorf("ran %v", f.eng.ran)
	}
}
