package budget

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/state"
)

// Name is the notice loop's name, as --loops spells it.
const Name = "budget"

// Capper lists the caps reached today; *Gate satisfies it.
type Capper interface {
	Capped(ctx context.Context) ([]Cap, time.Time, error)
}

// TokenCapper lists the token caps that block their role today; *Gate
// satisfies it.
type TokenCapper interface {
	TokenCapped(ctx context.Context) ([]TokenCap, time.Time, error)
}

// EventStore is the part of *state.Store the loop uses.
type EventStore interface {
	AppendEvent(ctx context.Context, e state.Event) error
	Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)
}

// Effects is the part of effects.Effects the loop uses.
type Effects interface {
	CreateComment(ctx context.Context, n int, body string) (github.Comment, error)
	DryRun() bool
}

// Deps are the loop's collaborators. StatusIssue returns 0 until the status
// issue is known. Now nil means time.Now; Log nil means slog.Default.
type Deps struct {
	Gate        Capper
	Tokens      TokenCapper // nil: no token notices
	Store       EventStore
	Effects     Effects
	StatusIssue func() int
	Log         *slog.Logger
	Now         func() time.Time
}

// Loop posts one status-issue comment per reached cap and UTC day. A
// budget_capped event written after the comment is the record for #58 and the
// marker that keeps a restart from posting the notice again.
type Loop struct {
	d  Deps
	mu sync.Mutex
}

// NewLoop builds the notice loop.
func NewLoop(d Deps) *Loop {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	return &Loop{d: d}
}

// Name implements controller.Loop.
func (*Loop) Name() string { return Name }

// Interval implements controller.Loop.
func (*Loop) Interval(*config.Resolved) time.Duration { return time.Minute }

// Claims is false: the notice is bookkeeping about work already done.
func (*Loop) Claims() bool { return false }

var _ controller.Loop = (*Loop)(nil)

type noticeKey struct{ scope, stage, role, day string }

// Tick posts the notices that are due.
func (l *Loop) Tick(ctx context.Context, _ controller.Env) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	issue := l.d.StatusIssue()
	if issue == 0 {
		return nil
	}
	caps, dayStart, err := l.d.Gate.Capped(ctx)
	if err != nil {
		return fmt.Errorf("budget: read spend: %w", err)
	}
	var tcaps []TokenCap
	if l.d.Tokens != nil {
		var tday time.Time
		if tcaps, tday, err = l.d.Tokens.TokenCapped(ctx); err != nil {
			return fmt.Errorf("budget: read tokens: %w", err)
		}
		if !tday.Equal(dayStart) {
			tcaps = nil
		}
	}
	if len(caps) == 0 && len(tcaps) == 0 {
		return nil
	}
	day := dayStart.Format("2006-01-02")
	evs, err := l.d.Store.Events(ctx, state.EventFilter{Kind: state.EventBudgetCapped, Since: dayStart})
	if err != nil {
		return fmt.Errorf("budget: read events: %w", err)
	}
	done := map[noticeKey]bool{}
	for _, e := range evs {
		var d struct {
			Scope string `json:"scope"`
			Day   string `json:"day"`
		}
		if json.Unmarshal(e.Data, &d) == nil {
			done[noticeKey{d.Scope, e.Stage, e.Role, d.Day}] = true
		}
	}
	for _, c := range caps {
		if done[noticeKey{c.Scope, c.Stage, c.Role, day}] {
			continue
		}
		if _, err := l.d.Effects.CreateComment(ctx, issue, Render(c, day)); err != nil {
			l.d.Log.Error("budget.notice_failed", "scope", c.Scope, "stage", c.Stage, "role", c.Role, "error", err)
			continue
		}
		data, _ := json.Marshal(map[string]any{
			"scope": c.Scope, "stage": c.Stage, "role": c.Role, "day": day,
			"spent_usd": c.Spent.USD, "cap_usd": c.Limit, "unpriced": c.Spent.Unpriced,
		})
		ev := state.Event{At: l.d.Now(), Kind: state.EventBudgetCapped, Stage: c.Stage, Role: c.Role, Data: data}
		if err := l.d.Store.AppendEvent(ctx, ev); err != nil {
			l.d.Log.Error("budget.notice_unrecorded", "scope", c.Scope, "stage", c.Stage, "role", c.Role, "error", err)
		}
	}
	for _, c := range tcaps {
		if done[noticeKey{"tokens", c.Stage, c.Role, day}] {
			continue
		}
		if _, err := l.d.Effects.CreateComment(ctx, issue, RenderTokens(c, day)); err != nil {
			l.d.Log.Error("budget.notice_failed", "scope", "tokens", "stage", c.Stage, "role", c.Role, "error", err)
			continue
		}
		data, _ := json.Marshal(map[string]any{
			"scope": "tokens", "stage": c.Stage, "role": c.Role, "day": day,
			"used": c.Used, "cap": c.Cap, "worst": c.Worst,
		})
		ev := state.Event{At: l.d.Now(), Kind: state.EventBudgetCapped, Stage: c.Stage, Role: c.Role, Data: data}
		if err := l.d.Store.AppendEvent(ctx, ev); err != nil {
			l.d.Log.Error("budget.notice_unrecorded", "scope", "tokens", "stage", c.Stage, "role", c.Role, "error", err)
		}
	}
	return nil
}

var safeName = regexp.MustCompile(`^[a-z0-9._/-]+$`)

func safe(s string) string {
	if safeName.MatchString(s) {
		return s
	}
	return "?"
}

// Render is the comment for c on the UTC day (2006-01-02). It holds only
// numbers, the day and config names; no agent text and no "@".
func Render(c Cap, day string) string {
	var b strings.Builder
	what := "The repo's daily budget"
	where := "the repo-wide `budget_usd_per_day`"
	scope := "repo"
	if c.Scope == "role" {
		stage, role := safe(c.Stage), safe(c.Role)
		what = fmt.Sprintf("The daily budget of %s role `%s`", stage, role)
		where = fmt.Sprintf("`budget_usd_per_day` of role `%s` (or the repo-wide value)", role)
		scope = "role " + stage + "/" + role
	}
	fmt.Fprintf(&b, "<!-- ard:budget scope=%s day=%s -->\n", strings.ReplaceAll(scope, " ", "-"), day)
	fmt.Fprintf(&b, "**ard · daily budget reached**\n\n")
	fmt.Fprintf(&b, "%s is reached for %s (UTC): $%.2f of $%.2f spent.", what, day, c.Spent.USD, c.Limit)
	if c.Spent.Unpriced > 0 {
		fmt.Fprintf(&b, " %d steps reported no cost and count as $0.", c.Spent.Unpriced)
	}
	b.WriteString("\n\nNo new jobs start under this cap; running jobs finish. On a subscription the dollar figure is an estimate.\n\n")
	fmt.Fprintf(&b, "To raise the cap, change %s in `.ard/config.yaml` through a reviewed pull request. The cap clears at 00:00 UTC.\n", where)
	return b.String()
}

// RenderTokens is the comment for a blocked token cap on the UTC day. It holds
// only numbers, the day and config names; no agent text and no "@".
func RenderTokens(c TokenCap, day string) string {
	stage, role := safe(c.Stage), safe(c.Role)
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- ard:budget cap=tokens role=%s/%s day=%s -->\n", stage, role, day)
	b.WriteString("**ard · daily token budget reached**\n\n")
	fmt.Fprintf(&b, "The daily token budget of %s role `%s` blocks new jobs for %s (UTC): %d of %d tokens used, largest recorded job %d tokens.", stage, role, day, c.Used, c.Cap, c.Worst)
	b.WriteString("\n\nNo new jobs of this role start under this cap; running jobs finish.\n\n")
	fmt.Fprintf(&b, "To raise the cap, change `tokens_per_day` of role `%s` (or the repo-wide value) in `.ard/config.yaml` through a reviewed pull request. The cap clears at 00:00 UTC.\n", role)
	return b.String()
}
