package servecmd

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

type budgetClock struct{ t time.Time }

func (c *budgetClock) now() time.Time { return c.t }

func budgetConfig() *config.Resolved {
	return &config.Resolved{BudgetUSDPerDay: 20, Roles: []config.Role{
		{Kind: config.KindWorker, Name: "eng", BudgetUSDPerDay: 5},
		{Kind: config.KindWorker, Name: "other"},
		{Kind: config.KindReviewer, Name: "rev"},
		{Kind: config.KindScanner, Name: "det", ScanPath: "x/scan"},
	}}
}

func budgetWiring(t *testing.T, dir string, clk *budgetClock) (*wiring, *state.Store) {
	t.Helper()
	ctx := context.Background()
	st, err := state.Open(ctx, dir, state.Options{Now: clk.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	w := &wiring{Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Store: st, budgetNow: clk.now}
	w.cfg.Store(budgetConfig())
	return w, st
}

func budgetPool(t *testing.T, w *wiring, st *state.Store) *pool.Pool {
	t.Helper()
	p, err := adoptPool(context.Background(), st, 4, w.Log, nil, w.budgetGate().Denied)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func spend(t *testing.T, st *state.Store, stage, role string, issue int, step string, usd float64) {
	t.Helper()
	j, err := st.CreateJob(context.Background(), state.NewJob{Stage: stage, Role: role, Issue: issue})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RecordStepUsage(context.Background(), j.ID, step, state.StepUsage{CostUSD: &usd}); err != nil {
		t.Fatal(err)
	}
}

func bc(c pool.Class, role string, n int) pool.Candidate {
	return pool.Candidate{Class: c, Role: role, Number: n, Since: time.Unix(1, 0)}
}

func verdict(p *pool.Pool, c pool.Candidate) pool.Verdict {
	return p.Offer("t", time.Minute, []pool.Candidate{c})[0].Verdict
}

func TestBudgetGateServeLevel(t *testing.T) {
	clk := &budgetClock{t: time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)}
	dir := t.TempDir()
	w, st := budgetWiring(t, dir, clk)
	p := budgetPool(t, w, st)

	// A job holds a slot before the caps are reached.
	slot := p.Offer("w", time.Minute, []pool.Candidate{bc(pool.ClassWorker, "eng", 1)})[0].Slot
	if slot == nil {
		t.Fatal("first offer not granted")
	}

	// Role cap: only that role is refused.
	spend(t, st, "worker", "eng", 1, "a", 5)
	if v := verdict(p, bc(pool.ClassWorker, "eng", 2)); v != pool.DeniedBudget {
		t.Errorf("eng at cap = %v", v)
	}
	if v := verdict(p, bc(pool.ClassWorker, "other", 3)); v != pool.Granted {
		t.Errorf("other = %v", v)
	}
	// The running job still finishes: releasing its slot works.
	slot.Release()

	// Repo cap: every agent role is refused, the deterministic scanner is not.
	spend(t, st, "worker", "other", 3, "a", 15)
	for _, c := range []pool.Candidate{bc(pool.ClassWorker, "other", 4), bc(pool.ClassReview, "rev", 5)} {
		if v := verdict(p, c); v != pool.DeniedBudget {
			t.Errorf("%v = %v, want budget", c, v)
		}
	}
	if v := verdict(p, bc(pool.ClassScanner, "det", 0)); v != pool.Granted {
		t.Errorf("deterministic scanner = %v", v)
	}

	// Restart: new wiring and pool on the same store still deny.
	w2, st2 := budgetWiring(t, dir, clk)
	st.Close()
	p2 := budgetPool(t, w2, st2)
	if v := verdict(p2, bc(pool.ClassReview, "rev", 6)); v != pool.DeniedBudget {
		t.Errorf("after restart = %v", v)
	}

	// 00:00 UTC clears the caps.
	clk.t = time.Date(2026, 3, 5, 0, 0, 0, 0, time.UTC)
	if v := verdict(p2, bc(pool.ClassReview, "rev", 7)); v != pool.Granted {
		t.Errorf("after midnight = %v", v)
	}
}

func TestBudgetGateFailsClosedOnReadError(t *testing.T) {
	clk := &budgetClock{t: time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)}
	w, st := budgetWiring(t, t.TempDir(), clk)
	p := budgetPool(t, w, st)
	st.Close()
	if v := verdict(p, bc(pool.ClassWorker, "other", 1)); v != pool.DeniedBudget {
		t.Errorf("closed store = %v, want budget", v)
	}
	if v := verdict(p, bc(pool.ClassScanner, "det", 0)); v != pool.Granted {
		t.Errorf("deterministic scanner = %v", v)
	}
}

func TestBudgetLoopNameAccepted(t *testing.T) {
	if _, err := selectLoops(nil, []string{"budget"}); err != nil {
		t.Fatal(err)
	}
}
