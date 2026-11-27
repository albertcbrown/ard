package servecmd

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/agent/claude"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/limits"
	"github.com/albertcbrown/ard/internal/modelauth"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/reaper"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

var limitT0 = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func limitWiring(t *testing.T, clk *fakeClock) *wiring {
	t.Helper()
	w := newTestWiring(t)
	book, err := limits.Open(context.Background(), w.Store, clk.now, w.Log)
	if err != nil {
		t.Fatal(err)
	}
	w.lim = book
	w.cfg.Store(&config.Resolved{Roles: []config.Role{
		{Kind: config.KindScanner, Name: "cve", ScanPath: "roles/scanner/cve/scan"},
		{Kind: config.KindScanner, Name: "llm-scan"},
		{Kind: config.KindTriage, Name: "lead"},
		{Kind: config.KindWorker, Name: "engineer", Model: "claude-opus-4-1"},
		{Kind: config.KindReviewer, Name: "sec", Model: "sonnet"},
	}})
	return w
}

func cand(c pool.Class, role string, n int) pool.Candidate {
	return pool.Candidate{Class: c, Role: role, Number: n, Since: limitT0.Add(-time.Hour)}
}

func TestLimitedFor(t *testing.T) {
	clk := &fakeClock{limitT0}
	w := limitWiring(t, clk)
	until, err := w.lim.Exhaust(context.Background(), "opus", limitT0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		c    pool.Candidate
		want time.Time
	}{
		{"deterministic scanner is never limited", cand(pool.ClassScanner, "cve", 0), time.Time{}},
		{"a scanner with no scan path uses a model: any entry blocks it", cand(pool.ClassScanner, "llm-scan", 0), until},
		{"opus role is blocked by opus", cand(pool.ClassWorker, "engineer", 5), until},
		{"sonnet role is not blocked by opus", cand(pool.ClassReview, "sec", 9), time.Time{}},
		{"a role with no model is blocked by any family", cand(pool.ClassTriage, "lead", 4), until},
		{"a role missing from the config is not limited", cand(pool.ClassWorker, "ghost", 5), time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := w.limitedFor(tc.c); !got.Equal(tc.want) {
				t.Errorf("limitedFor = %v, want %v", got, tc.want)
			}
		})
	}
	t.Run("no config loaded", func(t *testing.T) {
		w := newTestWiring(t)
		if got := w.limitedFor(cand(pool.ClassWorker, "engineer", 5)); !got.IsZero() {
			t.Errorf("limitedFor = %v", got)
		}
	})
}

// parse fixture -> classify -> record -> pool, end to end.
func TestLimitStopBlocksThePool(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		fixture string
		wait    time.Duration
	}{
		{"reset time in the stream", "synthetic_limit_rejected_event.jsonl", 0},
		{"no reset time: 30 minutes", "synthetic_api_retry_killed.jsonl", 30 * time.Minute},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clk := &fakeClock{limitT0}
			w := limitWiring(t, clk)
			f, err := os.Open(filepath.Join("..", "agent", "claude", "testdata", tc.fixture))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			res, err := claude.New(claude.Options{}).Parse(f, 1)
			if err != nil && !errors.Is(err, agent.ErrNoResult) {
				t.Fatal(err)
			}
			cause, ok := reaper.UsageLimitClassifier(agent.AuthOAuthToken)(res)
			if !ok {
				t.Fatal("not classified as a usage limit")
			}
			resets := cause.ResetsAt
			if tc.wait != 0 {
				resets = time.Time{}
			} else {
				clk.t = resets.Add(-2 * time.Hour) // the fixture's reset is in the future
			}
			until, err := w.lim.Exhaust(ctx, cause.Family, resets)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wait != 0 && !until.Equal(clk.t.Add(tc.wait)) {
				t.Fatalf("until = %v, want now+%v", until, tc.wait)
			}
			p2 := pool.New(pool.Options{Max: 2, Now: clk.now, Limited: w.limitedFor})
			c := cand(pool.ClassWorker, "engineer", 5)
			clk.t = until.Add(-time.Second)
			if ds := p2.Offer("worker", time.Minute, []pool.Candidate{c}); ds[0].Verdict != pool.DeniedLimited {
				t.Fatalf("before the reset: %v", ds[0].Verdict)
			}
			clk.t = until
			if ds := p2.Offer("worker", time.Minute, []pool.Candidate{c}); ds[0].Verdict != pool.Granted {
				t.Errorf("at the reset: %v", ds[0].Verdict)
			}
		})
	}
}

func TestReaperPartsWireLimits(t *testing.T) {
	w := newTestWiring(t)
	w.Model = modelauth.Credential{Auth: agent.AuthOAuthToken, Name: "CLAUDE_CODE_OAUTH_TOKEN"}
	f, err := w.finisher()
	if err != nil {
		t.Fatal(err)
	}
	if f.Limits == nil || f.Classify == nil {
		t.Fatalf("finisher = %+v", f)
	}
	book, _ := w.limitsBook(context.Background())
	if _, err := f.Limits.Exhaust(context.Background(), "opus", time.Now().Add(time.Hour)); err != nil || book.Until("opus").IsZero() {
		t.Errorf("the finisher and the pool use different books: %v", err)
	}
	// An OAuth credential makes a bare rate_limit signal a usage limit.
	res := agent.Result{LimitHit: &agent.LimitHit{Signal: agent.SignalRateLimitError}}
	if _, ok := f.Classify(res); !ok {
		t.Error("oauth: bare rate_limit not classified")
	}
	if b2, _ := w.limitsBook(context.Background()); b2 != book {
		t.Error("the book is not shared")
	}
}

func gateWiring(t *testing.T, clk *fakeClock, five, week float64) *wiring {
	t.Helper()
	w := newTestWiring(t)
	book, err := limits.Open(context.Background(), w.Store, clk.now, w.Log)
	if err != nil {
		t.Fatal(err)
	}
	w.lim = book
	w.cfg.Store(&config.Resolved{
		Agent: "claude",
		Limits: config.Limits{StopWorkersPct: 75, StopAllPct: 90, WeeklyStopAllPct: 90,
			WeeklyStopWorkersPctByDay: []int{20, 35, 50, 60, 70, 80, 85}},
		Roles: []config.Role{
			{Kind: config.KindScanner, Name: "cve", ScanPath: "roles/scanner/cve/scan", Agent: "claude"},
			{Kind: config.KindScanner, Name: "llm-scan", Agent: "claude"},
			{Kind: config.KindWorker, Name: "engineer", Agent: "claude", Model: "claude-opus-4-1"},
			{Kind: config.KindWorker, Name: "kimi", Agent: "kimi", Model: "sonnet"},
			{Kind: config.KindReviewer, Name: "sec", Agent: "claude", Model: "sonnet"},
		}})
	err = book.Observe(context.Background(), []agent.Window{
		{Type: "five_hour", Utilization: five, ResetsAt: clk.t.Add(2 * time.Hour)},
		{Type: "seven_day", Utilization: week, ResetsAt: clk.t.Add(6*24*time.Hour + 12*time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestLimitedForThresholds(t *testing.T) {
	clk := &fakeClock{limitT0}
	tests := []struct {
		name       string
		five, week float64
		c          pool.Candidate
		blocked    bool
		serial     bool
	}{
		{"5h 74: worker starts", 74, 0, cand(pool.ClassWorker, "engineer", 5), false, false},
		{"5h 75: worker held, serial", 75, 0, cand(pool.ClassWorker, "engineer", 5), true, true},
		{"5h 75: reviewer starts, serial", 75, 0, cand(pool.ClassReview, "sec", 9), false, true},
		{"5h 90: reviewer blocked", 90, 0, cand(pool.ClassReview, "sec", 9), true, false},
		{"5h 75: llm scanner held", 75, 0, cand(pool.ClassScanner, "llm-scan", 0), true, true},
		{"5h 95: deterministic scanner unaffected", 95, 95, cand(pool.ClassScanner, "cve", 0), false, false},
		{"5h 95: non-claude role unaffected", 95, 95, cand(pool.ClassWorker, "kimi", 5), false, false},
		// limitT0 is 1.5 days before the weekly reset: day 1.
		{"weekly day 1 at 19", 0, 19, cand(pool.ClassWorker, "engineer", 5), false, false},
		{"weekly day 1 at 20", 0, 20, cand(pool.ClassWorker, "engineer", 5), true, false},
		{"weekly 90: reviewer blocked", 0, 90, cand(pool.ClassReview, "sec", 9), true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := gateWiring(t, clk, tc.five, tc.week)
			if got := !w.limitedFor(tc.c).IsZero() && clk.t.Before(w.limitedFor(tc.c)); got != tc.blocked {
				t.Errorf("blocked = %v, want %v", got, tc.blocked)
			}
			a := w.annotate(tc.c)
			if a.Serial != tc.serial {
				t.Errorf("Serial = %v, want %v", a.Serial, tc.serial)
			}
			if wantAgent := tc.c.Role != "cve" && tc.c.Role != "kimi"; a.Agent != wantAgent {
				t.Errorf("Agent = %v, want %v", a.Agent, wantAgent)
			}
		})
	}
}

func TestOpusLimitBlocksOnlyOpusRoles(t *testing.T) {
	clk := &fakeClock{limitT0}
	w := gateWiring(t, clk, 10, 10)
	if _, err := w.lim.Exhaust(context.Background(), "opus", limitT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	blocked := func(c pool.Candidate) bool { return clk.t.Before(w.limitedFor(c)) }
	if !blocked(cand(pool.ClassWorker, "engineer", 5)) {
		t.Error("opus role must be blocked")
	}
	for _, c := range []pool.Candidate{cand(pool.ClassReview, "sec", 9), cand(pool.ClassScanner, "cve", 0), cand(pool.ClassWorker, "kimi", 6)} {
		if blocked(c) {
			t.Errorf("%s must start", c.Role)
		}
	}
}
