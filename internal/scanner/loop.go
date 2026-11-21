package scanner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/controller"
	"github.com/albertcbrown/ard/internal/state/scannerdb"
)

// PollInterval is how often the loop looks for a due scanner.
const PollInterval = 5 * time.Minute

// tickSlack is added to the longest scan timeout to bound one tick, which
// covers the reconcile, the filing and the commits around a scan.
const tickSlack = 15 * time.Minute

// Builtin is a scanner with no role directory. (The stale closer, #25, is not
// one: it is a plain controller loop.) It needs no slot, executor or worktree. The loop schedules it from
// its own database and records its runs there.
type Builtin interface {
	Name() string
	Every() time.Duration
	Run(ctx context.Context) error
}

// Loop is the scanners loop. It runs at most one due scanner per tick, the
// most overdue. A run that was denied a pool slot is not recorded, so the
// scanner stays due and is offered again on the next tick. The loop claims
// work (it files and closes issues), so the controller skips it while paused.
type Loop struct {
	p        *Pipeline
	builtins []Builtin
}

var (
	_ controller.Loop          = (*Loop)(nil)
	_ controller.TickTimeouter = (*Loop)(nil)
)

// NewLoop returns the scanners loop over p.
func NewLoop(p *Pipeline, builtins ...Builtin) *Loop { return &Loop{p: p, builtins: builtins} }

func (l *Loop) Name() string                            { return "scanners" }
func (l *Loop) Claims() bool                            { return true }
func (l *Loop) Interval(*config.Resolved) time.Duration { return PollInterval }

// TickTimeout bounds a tick by the longest scan timeout plus slack, so a long
// scan is not cancelled at the poll interval.
func (l *Loop) TickTimeout(cfg *config.Resolved) time.Duration {
	var longest time.Duration
	for _, r := range cfg.RolesOf(config.KindScanner) {
		if r.ScanPath != "" {
			longest = max(longest, r.Timeout)
		}
	}
	if longest == 0 {
		return 0
	}
	return longest + tickSlack
}

func (l *Loop) builtinNamed(name string) bool {
	for _, b := range l.builtins {
		if b.Name() == name {
			return true
		}
	}
	return false
}

type dueScanner struct {
	due     time.Time // zero when it never ran
	role    config.Role
	builtin Builtin
}

func (d dueScanner) name() string {
	if d.builtin != nil {
		return d.builtin.Name()
	}
	return d.role.Name
}

// Tick runs the most overdue scanner, if any is due.
func (l *Loop) Tick(ctx context.Context, env controller.Env) error {
	cfg := env.Cfg
	now := l.p.now()
	var best *dueScanner
	consider := func(c dueScanner, every time.Duration) error {
		store, err := l.p.Stores(ctx, c.name())
		if err != nil {
			return fmt.Errorf("scanner %s: open state: %w", c.name(), err)
		}
		last, err := store.LastRun(ctx)
		switch {
		case errors.Is(err, scannerdb.ErrNotFound):
		case err != nil:
			return fmt.Errorf("scanner %s: last run: %w", c.name(), err)
		default:
			c.due = last.StartedAt.Add(every)
			if c.due.After(now) {
				return nil
			}
		}
		if best == nil || c.due.Before(best.due) {
			best = &c
		}
		return nil
	}
	var errs []error
	for _, r := range cfg.RolesOf(config.KindScanner) {
		switch {
		case r.ScanPath == "":
			env.Log.Debug("scanner.skip", "scanner", r.Name, "reason", "LLM scanner (phase 2)")
			continue
		case l.builtinNamed(r.Name):
			env.Log.Warn("scanner.skip", "scanner", r.Name, "reason", "name collides with a built-in scanner")
			continue
		}
		if err := consider(dueScanner{role: r}, r.Every); err != nil {
			errs = append(errs, err)
		}
	}
	for _, b := range l.builtins {
		if err := consider(dueScanner{builtin: b}, b.Every()); err != nil {
			errs = append(errs, err)
		}
	}
	if best != nil {
		if err := l.run(ctx, env, cfg, *best, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (l *Loop) run(ctx context.Context, env controller.Env, cfg *config.Resolved, d dueScanner, now time.Time) error {
	if d.builtin != nil {
		return l.runBuiltin(ctx, env, d.builtin)
	}
	p := *l.p // per-tick copy so the run logs under the loop's tick tag
	p.Log = env.Log
	since := d.due
	if since.IsZero() {
		since = now
	}
	_, err := p.Run(ctx, cfg, d.role, since)
	return err
}

func (l *Loop) runBuiltin(ctx context.Context, env controller.Env, b Builtin) error {
	store, err := l.p.Stores(ctx, b.Name())
	if err != nil {
		return err
	}
	start := l.p.now()
	rerr := b.Run(ctx)
	outcome := OutcomeOK
	if rerr != nil {
		outcome = OutcomeError
	}
	if _, err := store.RecordRun(context.WithoutCancel(ctx), scannerdb.Run{StartedAt: start, FinishedAt: l.p.now(), Outcome: outcome}); err != nil {
		env.Log.Error("scanner.record_run_failed", "scanner", b.Name(), "error", err)
	}
	if rerr != nil {
		return fmt.Errorf("built-in scanner %s: %w", b.Name(), rerr)
	}
	return nil
}
