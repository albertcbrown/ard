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
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

type fakeTokens struct {
	byDay    map[time.Time]map[state.RoleKey]int64
	worst    map[state.RoleKey]int64
	err      error
	worstErr error
}

func (f *fakeTokens) RoleTokens(_ context.Context, d time.Time) (map[state.RoleKey]int64, error) {
	return f.byDay[d], f.err
}
func (f *fakeTokens) RoleWorstTokens(_ context.Context, k state.RoleKey) (int64, error) {
	return f.worst[k], f.worstErr
}

var (
	day4   = time.Date(2026, 3, 4, 0, 0, 0, 0, time.UTC)
	day5   = day4.AddDate(0, 0, 1)
	engKey = state.RoleKey{Stage: "worker", Role: "eng"}
	revKey = state.RoleKey{Stage: "review", Role: "rev"}
)

func tokCfg() *config.Resolved {
	return &config.Resolved{Roles: []config.Role{
		{Kind: config.KindWorker, Name: "eng", TokensPerDay: 100},
		{Kind: config.KindReviewer, Name: "rev", TokensPerDay: 100},
		{Kind: config.KindWorker, Name: "free"},
		{Kind: config.KindScanner, Name: "det", ScanPath: "x"},
	}}
}

func tokGate(cfg func() *config.Resolved, sp SpendReader, tr *fakeTokens) (*Gate, *bytes.Buffer, *time.Time) {
	now := time.Date(2026, 3, 4, 23, 59, 59, 0, time.UTC)
	var buf bytes.Buffer
	g := New(sp, cfg, func() time.Time { return now }, slog.New(slog.NewTextHandler(&buf, nil)))
	g.SetTokenReader(tr)
	return g, &buf, &now
}

func TestTokenRule(t *testing.T) {
	for _, c := range []struct {
		u, cp, w int64
		block    bool
	}{
		{0, 100, 50, false}, {50, 100, 50, false}, {51, 100, 50, true},
		{100, 100, 0, true}, {0, 100, 500, false}, {1, 100, 500, true},
		{99, 100, 0, false}, {101, 100, 0, true},
	} {
		if got := tokenBlocked(c.u, c.cp, c.w); got != c.block {
			t.Errorf("U=%d C=%d W=%d: got %v", c.u, c.cp, c.w, got)
		}
	}
}

func TestTokenBlocksOnlyThatRole(t *testing.T) {
	tr := &fakeTokens{byDay: map[time.Time]map[state.RoleKey]int64{day4: {engKey: 60, revKey: 10}}, worst: map[state.RoleKey]int64{engKey: 50, revKey: 50}}
	g, buf, _ := tokGate(tokCfg, &fakeReader{}, tr)
	if d, why := g.Denied(cand(pool.ClassWorker, "eng")); !d || !strings.Contains(why, "token") {
		t.Errorf("eng: %v %q", d, why)
	}
	if d, _ := g.Denied(cand(pool.ClassReview, "rev")); d {
		t.Error("rev must run")
	}
	if d, _ := g.Denied(cand(pool.ClassWorker, "free")); d {
		t.Error("uncapped role must run")
	}
	g.Denied(cand(pool.ClassWorker, "eng"))
	if n := strings.Count(buf.String(), "budget.blocked"); n != 1 {
		t.Errorf("budget.blocked logged %d times", n)
	}
}

func TestKimiTokenCapNoDollar(t *testing.T) {
	cfg := func() *config.Resolved {
		return &config.Resolved{Roles: []config.Role{{Kind: config.KindWorker, Name: "eng", TokensPerDay: 100}}}
	}
	tr := &fakeTokens{byDay: map[time.Time]map[state.RoleKey]int64{day4: {engKey: 100}}}
	g, _, _ := tokGate(cfg, &fakeReader{}, tr)
	if d, _ := g.Denied(cand(pool.ClassWorker, "eng")); !d {
		t.Error("token cap alone must refuse")
	}
}

func TestRepoTokenDefault(t *testing.T) {
	cfg := func() *config.Resolved {
		return &config.Resolved{TokensPerDay: 100, Roles: []config.Role{{Kind: config.KindWorker, Name: "eng"}}}
	}
	tr := &fakeTokens{byDay: map[time.Time]map[state.RoleKey]int64{day4: {engKey: 100}}}
	g, _, _ := tokGate(cfg, &fakeReader{}, tr)
	if d, _ := g.Denied(cand(pool.ClassWorker, "eng")); !d {
		t.Error("repo default must apply")
	}
}

func TestBothCaps(t *testing.T) {
	cfg := func() *config.Resolved {
		return &config.Resolved{Roles: []config.Role{{Kind: config.KindWorker, Name: "eng", TokensPerDay: 100, BudgetUSDPerDay: 10}}}
	}
	for _, c := range []struct {
		usd  float64
		tok  int64
		deny bool
	}{{10, 0, true}, {0, 100, true}, {0, 0, false}} {
		r := &fakeReader{per: map[state.RoleKey]state.Spend{engKey: {USD: c.usd}}}
		tr := &fakeTokens{byDay: map[time.Time]map[state.RoleKey]int64{day4: {engKey: c.tok}}}
		g, _, _ := tokGate(cfg, r, tr)
		if d, _ := g.Denied(cand(pool.ClassWorker, "eng")); d != c.deny {
			t.Errorf("usd=%v tok=%d: denied=%v", c.usd, c.tok, d)
		}
	}
}

func TestScannerExempt(t *testing.T) {
	tr := &fakeTokens{err: errors.New("boom")}
	g, _, _ := tokGate(tokCfg, &fakeReader{}, tr)
	if d, _ := g.Denied(cand(pool.ClassScanner, "det")); d {
		t.Error("deterministic scanner must pass")
	}
}

func TestTokenReadFailsClosed(t *testing.T) {
	for name, tr := range map[string]*fakeTokens{
		"usage": {err: errors.New("boom")},
		"worst": {worstErr: errors.New("boom")},
	} {
		g, buf, _ := tokGate(tokCfg, &fakeReader{}, tr)
		d, why := g.Denied(cand(pool.ClassWorker, "eng"))
		if !d || !strings.Contains(why, "budget state unreadable") {
			t.Errorf("%s: %v %q", name, d, why)
		}
		if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "budget.read_failed") {
			t.Errorf("%s: log %q", name, buf.String())
		}
	}
}

func TestTokenResetAtMidnightUTC(t *testing.T) {
	tr := &fakeTokens{byDay: map[time.Time]map[state.RoleKey]int64{day4: {engKey: 100}}}
	g, _, now := tokGate(tokCfg, &fakeReader{}, tr)
	if d, _ := g.Denied(cand(pool.ClassWorker, "eng")); !d {
		t.Error("23:59:59 must block")
	}
	*now = day5
	if d, _ := g.Denied(cand(pool.ClassWorker, "eng")); d {
		t.Error("00:00:00 next day must allow")
	}
}

func TestBlockedLoggedOncePerDay(t *testing.T) {
	tr := &fakeTokens{byDay: map[time.Time]map[state.RoleKey]int64{day4: {engKey: 100}, day5: {engKey: 100}}}
	g, buf, now := tokGate(tokCfg, &fakeReader{}, tr)
	g.Denied(cand(pool.ClassWorker, "eng"))
	g.Denied(cand(pool.ClassWorker, "eng"))
	*now = day5.Add(time.Hour)
	g.Denied(cand(pool.ClassWorker, "eng"))
	if n := strings.Count(buf.String(), "budget.blocked"); n != 2 {
		t.Errorf("logged %d times", n)
	}
}

func TestNilTokenReader(t *testing.T) {
	g, _, _ := tokGate(tokCfg, &fakeReader{}, nil)
	g.tokens = nil
	if d, _ := g.Denied(cand(pool.ClassWorker, "eng")); d {
		t.Error("nil reader means no token check")
	}
}

func TestTokenReportAndCapped(t *testing.T) {
	tr := &fakeTokens{byDay: map[time.Time]map[state.RoleKey]int64{day4: {engKey: 90, revKey: 10}}, worst: map[state.RoleKey]int64{engKey: 20, revKey: 20}}
	g, _, _ := tokGate(tokCfg, &fakeReader{}, tr)
	rep, day, err := g.TokenReport(context.Background())
	if err != nil || !day.Equal(day4) || len(rep) != 2 {
		t.Fatalf("%v %v %v", rep, day, err)
	}
	if rep[0] != (TokenCap{Stage: "worker", Role: "eng", Used: 90, Cap: 100, Worst: 20}) || rep[1].Role != "rev" {
		t.Errorf("report %+v", rep)
	}
	bl, _, _ := g.TokenCapped(context.Background())
	if len(bl) != 1 || bl[0].Role != "eng" {
		t.Errorf("capped %+v", bl)
	}
}

// Notice tests.

type fakeTokCapper struct {
	caps []TokenCap
	now  *time.Time
}

func (f *fakeTokCapper) TokenCapped(context.Context) ([]TokenCap, time.Time, error) {
	return f.caps, utcDayStart(*f.now), nil
}

func tokNoticeLoop(e *noticeEnv, caps ...TokenCap) *Loop {
	return NewLoop(Deps{Gate: e.capper, Tokens: &fakeTokCapper{caps: caps, now: &e.now}, Store: e.st, Effects: e.fx,
		StatusIssue: func() int { return e.issue }, Now: func() time.Time { return e.now }})
}

var engTok = TokenCap{Stage: "worker", Role: "eng", Used: 95, Cap: 100, Worst: 20}

func TestTokenNoticeOncePerDay(t *testing.T) {
	e := newNoticeEnv(t)
	l := tokNoticeLoop(e, engTok)
	e.tick(t, l)
	e.tick(t, l)
	if len(e.fx.bodies) != 1 {
		t.Fatalf("comments %d", len(e.fx.bodies))
	}
	e.now = e.now.Add(24 * time.Hour)
	e.tick(t, l)
	if len(e.fx.bodies) != 2 {
		t.Fatalf("next day comments %d", len(e.fx.bodies))
	}
}

func TestTokenNoticeMarker(t *testing.T) {
	b := RenderTokens(engTok, "2026-03-04")
	if !strings.HasPrefix(b, "<!-- ard:budget cap=tokens role=worker/eng day=2026-03-04 -->\n") || strings.Contains(b, "@") {
		t.Errorf("body %q", b)
	}
	if b := RenderTokens(TokenCap{Stage: "worker", Role: "Ev@l"}, "2026-03-04"); !strings.Contains(b, "role=worker/? ") || strings.Contains(b, "@") {
		t.Errorf("unsafe %q", b)
	}
}

func TestTokenNoticeRestart(t *testing.T) {
	e := newNoticeEnv(t)
	e.tick(t, tokNoticeLoop(e, engTok))
	e.tick(t, tokNoticeLoop(e, engTok))
	if len(e.fx.bodies) != 1 {
		t.Fatalf("comments %d", len(e.fx.bodies))
	}
}

func TestTokenAndDollarNoticesIndependent(t *testing.T) {
	e := newNoticeEnv(t, engCap)
	e.tick(t, tokNoticeLoop(e, engTok))
	if len(e.fx.bodies) != 2 {
		t.Fatalf("comments %d", len(e.fx.bodies))
	}
}

var _ = controller.Env{}
