package budget

import (
	"context"
	"fmt"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/state"
)

// TokenReader reads recorded token usage; *state.Store satisfies it.
type TokenReader interface {
	RoleTokens(ctx context.Context, dayStart time.Time) (map[state.RoleKey]int64, error)
	RoleWorstTokens(ctx context.Context, k state.RoleKey) (int64, error)
}

// SetTokenReader wires the token check. Without a reader no token cap is
// checked. Call it before the gate is used.
func (g *Gate) SetTokenReader(r TokenReader) { g.tokens = r }

// tokenBlocked is the claim rule: with U tokens used today, cap C and worst
// case W (the role's largest recorded job total), refuse when U >= C or
// C-U < min(W, C). Running jobs reserve nothing.
func tokenBlocked(used, limit, worst int64) bool {
	return used >= limit || limit-used < min(worst, limit)
}

// tokenFigures reads U and W for one role.
func (g *Gate) tokenFigures(ctx context.Context, dayStart time.Time, k state.RoleKey) (used, worst int64, err error) {
	per, err := g.tokens.RoleTokens(ctx, dayStart)
	if err != nil {
		return 0, 0, err
	}
	worst, err = g.tokens.RoleWorstTokens(ctx, k)
	if err != nil {
		return 0, 0, err
	}
	return per[k], worst, nil
}

// tokenDenied applies the token rule to one candidate role.
func (g *Gate) tokenDenied(ctx context.Context, cfg *config.Resolved, role config.Role, stage string, dayStart time.Time) (bool, string) {
	if g.tokens == nil {
		return false, ""
	}
	limit, set := cfg.RoleTokensPerDay(role)
	if !set {
		return false, ""
	}
	used, worst, err := g.tokenFigures(ctx, dayStart, state.RoleKey{Stage: stage, Role: role.Name})
	if err != nil {
		g.log.Warn("budget.read_failed", "cap", "tokens", "role", stage+"/"+role.Name, "error", err)
		return true, "budget state unreadable"
	}
	if !tokenBlocked(used, limit, worst) {
		return false, ""
	}
	g.mu.Lock()
	if !g.day.Equal(dayStart) {
		g.day, g.logged = dayStart, map[string]bool{}
	}
	key := "tokens|" + stage + "/" + role.Name
	first := !g.logged[key]
	g.logged[key] = true
	g.mu.Unlock()
	if first {
		g.log.Warn("budget.blocked", "cap", "tokens", "role", stage+"/"+role.Name, "used", used, "limit", limit, "worst", worst)
	}
	return true, fmt.Sprintf("role daily token budget reached: %d of %d tokens used today (UTC), worst job %d", used, limit, worst)
}

// TokenCap is one role's token cap and today's figures.
type TokenCap struct {
	Stage, Role      string
	Used, Cap, Worst int64
}

// Blocked reports whether the claim rule refuses the role now.
func (c TokenCap) Blocked() bool { return tokenBlocked(c.Used, c.Cap, c.Worst) }

// TokenReport returns every role with a token cap, blocked or not, in config
// order, and the UTC day start. It logs nothing.
func (g *Gate) TokenReport(ctx context.Context) ([]TokenCap, time.Time, error) {
	dayStart := utcDayStart(g.now())
	cfg := g.cfg()
	if cfg == nil || g.tokens == nil {
		return nil, dayStart, nil
	}
	var out []TokenCap
	var per map[state.RoleKey]int64
	for _, role := range cfg.Roles {
		if role.Kind == config.KindScanner && role.ScanPath != "" {
			continue
		}
		stage, ok := stageOf(role.Kind)
		if !ok {
			continue
		}
		limit, set := cfg.RoleTokensPerDay(role)
		if !set {
			continue
		}
		if per == nil {
			var err error
			if per, err = g.tokens.RoleTokens(ctx, dayStart); err != nil {
				return nil, dayStart, err
			}
		}
		k := state.RoleKey{Stage: stage, Role: role.Name}
		worst, err := g.tokens.RoleWorstTokens(ctx, k)
		if err != nil {
			return nil, dayStart, err
		}
		out = append(out, TokenCap{Stage: stage, Role: role.Name, Used: per[k], Cap: limit, Worst: worst})
	}
	return out, dayStart, nil
}

// TokenCapped returns only the blocked token caps. It logs nothing.
func (g *Gate) TokenCapped(ctx context.Context) ([]TokenCap, time.Time, error) {
	all, day, err := g.TokenReport(ctx)
	if err != nil {
		return nil, day, err
	}
	var out []TokenCap
	for _, c := range all {
		if c.Blocked() {
			out = append(out, c)
		}
	}
	return out, day, nil
}

func stageOf(k config.Kind) (string, bool) {
	switch k {
	case config.KindScanner:
		return "scanner", true
	case config.KindTriage:
		return "triage", true
	case config.KindWorker:
		return "worker", true
	case config.KindReviewer:
		return "review", true
	}
	return "", false
}
