package budget

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

type fakeReader struct {
	per   map[state.RoleKey]state.Spend
	repo  state.Spend
	err   error
	since time.Time
}

func (f *fakeReader) SpendSince(_ context.Context, d time.Time) (map[state.RoleKey]state.Spend, state.Spend, error) {
	f.since = d
	return f.per, f.repo, f.err
}

func testCfg() *config.Resolved {
	return &config.Resolved{BudgetUSDPerDay: 50, Roles: []config.Role{
		{Kind: config.KindWorker, Name: "eng", BudgetUSDPerDay: 10},
		{Kind: config.KindWorker, Name: "free"},
		{Kind: config.KindReviewer, Name: "rev", BudgetUSDPerDay: 5},
		{Kind: config.KindScanner, Name: "det", ScanPath: ".ard/scanners/det/scan"},
		{Kind: config.KindScanner, Name: "llm", PromptPath: "p"},
	}}
}

func cand(c pool.Class, role string) pool.Candidate {
	return pool.Candidate{Class: c, Role: role, Number: 1, Since: time.Unix(1, 0)}
}

func setup(r *fakeReader) (*Gate, *bytes.Buffer, *time.Time) {
	now := time.Date(2026, 3, 4, 23, 59, 0, 0, time.UTC)
	var buf bytes.Buffer
	g := New(r, testCfg, func() time.Time { return now }, slog.New(slog.NewTextHandler(&buf, nil)))
	return g, &buf, &now
}

func TestRoleCap(t *testing.T) {
	r := &fakeReader{per: map[state.RoleKey]state.Spend{{Stage: "worker", Role: "eng"}: {USD: 9.99}}, repo: state.Spend{USD: 9.99}}
	g, _, _ := setup(r)
	if d, _ := g.Denied(cand(pool.ClassWorker, "eng")); d {
		t.Error("just under cap must pass")
	}
	r.per[state.RoleKey{Stage: "worker", Role: "eng"}] = state.Spend{USD: 10, Unpriced: 2}
	d, why := g.Denied(cand(pool.ClassWorker, "eng"))
	if !d || !strings.Contains(why, "2 unpriced") {
		t.Errorf("spent == cap: %v %q", d, why)
	}
	if d, _ := g.Denied(cand(pool.ClassWorker, "free")); d {
		t.Error("role cap must not deny another role")
	}
	if !r.since.Equal(time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day start = %v", r.since)
	}
}

func TestRepoCap(t *testing.T) {
	g, _, _ := setup(&fakeReader{repo: state.Spend{USD: 50}})
	for _, c := range []pool.Candidate{cand(pool.ClassWorker, "free"), cand(pool.ClassReview, "rev"), cand(pool.ClassScanner, "llm")} {
		if d, _ := g.Denied(c); !d {
			t.Errorf("%v must be denied at the repo cap", c)
		}
	}
	if d, _ := g.Denied(cand(pool.ClassScanner, "det")); d {
		t.Error("deterministic scanner must be exempt")
	}
	if d, _ := g.Denied(cand(pool.ClassWorker, "gone")); d {
		t.Error("role missing from config is exempt")
	}
}

func TestReaderErrorFailsClosed(t *testing.T) {
	g, _, _ := setup(&fakeReader{err: errors.New("db")})
	if d, _ := g.Denied(cand(pool.ClassWorker, "free")); !d {
		t.Error("read error must deny")
	}
	if d, _ := g.Denied(cand(pool.ClassScanner, "det")); d {
		t.Error("deterministic scanner needs no read")
	}
}

func TestCappedLoggedOncePerDay(t *testing.T) {
	g, buf, now := setup(&fakeReader{repo: state.Spend{USD: 60}})
	for range 3 {
		g.Denied(cand(pool.ClassWorker, "free"))
	}
	if n := strings.Count(buf.String(), "budget.capped"); n != 1 {
		t.Fatalf("logged %d times, want 1", n)
	}
	*now = now.Add(2 * time.Minute) // past 00:00 UTC
	g.Denied(cand(pool.ClassWorker, "free"))
	if n := strings.Count(buf.String(), "budget.capped"); n != 2 {
		t.Fatalf("logged %d times after rollover, want 2", n)
	}
}

func TestCapped(t *testing.T) {
	r := &fakeReader{
		per: map[state.RoleKey]state.Spend{
			{Stage: "worker", Role: "eng"}:    {USD: 10},
			{Stage: "review", Role: "rev"}:    {USD: 4.99},
			{Stage: "scanner", Role: "det"}:   {USD: 99},
			{Stage: "worker", Role: "absent"}: {USD: 99},
		},
		repo: state.Spend{USD: 50, Unpriced: 1},
	}
	g, buf, _ := setup(r)
	caps, day, err := g.Capped(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 2 || caps[0].Scope != "repo" || caps[1].Stage != "worker" || caps[1].Role != "eng" || caps[0].Spent.Unpriced != 1 {
		t.Fatalf("caps %+v", caps)
	}
	if !day.Equal(time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day %v", day)
	}
	if buf.Len() != 0 {
		t.Errorf("Capped logged: %s", buf)
	}
	r.err = errors.New("db")
	if _, _, err := g.Capped(context.Background()); err == nil {
		t.Error("read error must surface")
	}
}
