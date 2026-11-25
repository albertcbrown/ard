// Package budget enforces the daily dollar and token caps at the pool's claim
// gate. A job must pass both checks; the token rule is in tokens.go.
//
// The day is the UTC day. A candidate is refused when spent >= cap, for its
// role's own cap (budget_usd_per_day on the role) or the repo cap. Steps that
// reported no cost count as $0 and their number is shown. Reviewers get no
// exemption; deterministic scanners cost nothing and are exempt. A job that
// already holds a slot is never touched: the gate only sees new candidates.
// A state read failure denies (fails closed).
package budget

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

// SpendReader reads today's spend; *state.Store satisfies it.
type SpendReader interface {
	SpendSince(ctx context.Context, dayStart time.Time) (map[state.RoleKey]state.Spend, state.Spend, error)
}

// Gate is the pool's Options.Budget hook.
type Gate struct {
	store  SpendReader
	tokens TokenReader // nil: token caps are not wired
	cfg    func() *config.Resolved
	now    func() time.Time
	log    *slog.Logger

	mu     sync.Mutex
	day    time.Time       // UTC day start the logged map belongs to
	logged map[string]bool // caps already logged on day
}

// New builds a Gate. cfg returns the latest config (nil: nothing is capped);
// now nil means time.Now; log nil means slog.Default.
func New(store SpendReader, cfg func() *config.Resolved, now func() time.Time, log *slog.Logger) *Gate {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Gate{store: store, cfg: cfg, now: now, log: log, logged: map[string]bool{}}
}

func kindOf(c pool.Class) (config.Kind, string, bool) {
	switch c {
	case pool.ClassScanner:
		return config.KindScanner, c.String(), true
	case pool.ClassTriage:
		return config.KindTriage, c.String(), true
	case pool.ClassWorker:
		return config.KindWorker, c.String(), true
	case pool.ClassReview:
		return config.KindReviewer, c.String(), true
	}
	return "", "", false
}

// Denied reports whether a cap forbids starting c, and why.
func (g *Gate) Denied(c pool.Candidate) (bool, string) {
	kind, stage, ok := kindOf(c.Class)
	if !ok {
		return false, ""
	}
	cfg := g.cfg()
	if cfg == nil {
		return false, ""
	}
	role, ok := cfg.Role(kind, c.Role)
	if !ok || (kind == config.KindScanner && role.ScanPath != "") {
		return false, ""
	}
	dayStart := utcDayStart(g.now())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	per, repo, err := g.store.SpendSince(ctx, dayStart)
	if err != nil {
		g.log.Error("budget.read_failed", "error", err)
		return true, "budget: spend unreadable, failing closed"
	}
	if cfg.BudgetUSDPerDay > 0 && repo.USD >= cfg.BudgetUSDPerDay {
		return true, g.capped(dayStart, "repo", "", repo, cfg.BudgetUSDPerDay)
	}
	if limit, set := cfg.RoleBudgetUSDPerDay(role); set {
		sp := per[state.RoleKey{Stage: stage, Role: c.Role}]
		if sp.USD >= limit {
			return true, g.capped(dayStart, "role", stage+"/"+c.Role, sp, limit)
		}
	}
	return g.tokenDenied(ctx, cfg, role, stage, dayStart)
}

// capped builds the reason and logs budget.capped once per cap and UTC day.
func (g *Gate) capped(dayStart time.Time, scope, role string, sp state.Spend, limit float64) string {
	reason := fmt.Sprintf("%s daily budget reached: $%.2f of $%.2f spent today (UTC)", scope, sp.USD, limit)
	if sp.Unpriced > 0 {
		reason += fmt.Sprintf(", %d unpriced steps counted as $0", sp.Unpriced)
	}
	g.mu.Lock()
	if !g.day.Equal(dayStart) {
		g.day, g.logged = dayStart, map[string]bool{}
	}
	key := scope + "|" + role
	first := !g.logged[key]
	g.logged[key] = true
	g.mu.Unlock()
	if first {
		g.log.Warn("budget.capped", "scope", scope, "role", role, "spent_usd", sp.USD, "cap_usd", limit, "unpriced", sp.Unpriced)
	}
	return reason
}

// utcDayStart is 00:00 UTC of t's UTC day: the one day rule of the gate and
// the notice loop.
func utcDayStart(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// Cap is one reached cap. Scope is "repo" or "role"; Stage and Role are empty
// for the repo cap.
type Cap struct {
	Scope, Stage, Role string
	Spent              state.Spend
	Limit              float64
}

// Capped returns every cap reached today (repo first, then roles in config
// order) and the UTC day start. It applies the rules of Denied, logs nothing.
func (g *Gate) Capped(ctx context.Context) ([]Cap, time.Time, error) {
	dayStart := utcDayStart(g.now())
	cfg := g.cfg()
	if cfg == nil {
		return nil, dayStart, nil
	}
	per, repo, err := g.store.SpendSince(ctx, dayStart)
	if err != nil {
		return nil, dayStart, err
	}
	var out []Cap
	if cfg.BudgetUSDPerDay > 0 && repo.USD >= cfg.BudgetUSDPerDay {
		out = append(out, Cap{Scope: "repo", Spent: repo, Limit: cfg.BudgetUSDPerDay})
	}
	stages := map[config.Kind]string{config.KindScanner: "scanner", config.KindTriage: "triage", config.KindWorker: "worker", config.KindReviewer: "review"}
	for _, role := range cfg.Roles {
		if role.Kind == config.KindScanner && role.ScanPath != "" {
			continue
		}
		stage, ok := stages[role.Kind]
		if !ok {
			continue
		}
		if limit, set := cfg.RoleBudgetUSDPerDay(role); set {
			sp := per[state.RoleKey{Stage: stage, Role: role.Name}]
			if sp.USD >= limit {
				out = append(out, Cap{Scope: "role", Stage: stage, Role: role.Name, Spent: sp, Limit: limit})
			}
		}
	}
	return out, dayStart, nil
}
