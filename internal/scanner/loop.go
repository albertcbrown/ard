package scanner

import (
	"context"
	"errors"
	"fmt"
	"sort"
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
// most overdue. A run that was denied a pool slot, skipped by its engine or
// stopped by a usage limit is not recorded, so the scanner stays due and is
// offered again on the next tick. The loop claims work (it files and closes
// issues), so the controller skips it while paused.
type Loop struct {
	p        *Pipeline
	builtins []Builtin
	// warned holds "<scanner> <reason>" for engine skips already logged at
	// Warn; later ones log at Debug. Only Tick touches it, and ticks of one
	// loop never overlap.
	warned map[string]bool
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

// TickTimeout bounds a tick by the longest scan timeout, LLM scanners
// included, plus slack, so a long scan is not cancelled at the poll interval.
func (l *Loop) TickTimeout(cfg *config.Resolved) time.Duration {
	var longest time.Duration
	for _, r := range cfg.RolesOf(config.KindScanner) {
		longest = max(longest, r.Timeout)
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

// Tick runs due scanners, most overdue first, and stops after the first one
// that did anything but get denied a pool slot, skipped or stopped by a usage
// limit. Such a scanner stays the most overdue; without the walk it would
// starve the built-ins and every other scanner. A scanner whose engine is not
// ready (no model credential, an executor without LLM support) is left out
// before its state is read, so it costs no GitHub call.
func (l *Loop) Tick(ctx context.Context, env controller.Env) error {
	cfg := env.Cfg
	now := l.p.now()
	var due []dueScanner
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
		due = append(due, c)
		return nil
	}
	var errs []error
	for _, r := range cfg.RolesOf(config.KindScanner) {
		if l.builtinNamed(r.Name) {
			env.Log.Warn("scanner.skip", "scanner", r.Name, "reason", "name collides with a built-in scanner")
			continue
		}
		if err := notReady(l.p.Engine, r); err != nil {
			l.logSkip(env, r.Name, err)
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
	sort.SliceStable(due, func(i, j int) bool { return due[i].due.Before(due[j].due) })
	for _, d := range due {
		outcome, err := l.run(ctx, env, cfg, d, now)
		if err != nil {
			errs = append(errs, err)
		}
		if outcome != OutcomeNoSlot && outcome != OutcomeSkipped && outcome != OutcomeLimited {
			break
		}
	}
	return errors.Join(errs...)
}

// logSkip logs an engine skip at Warn once per scanner and reason for the life
// of the loop, then at Debug, so a missing credential does not warn every tick.
func (l *Loop) logSkip(env controller.Env, name string, err error) {
	reason := skipReason(err)
	key := name + " " + reason
	if l.warned[key] {
		env.Log.Debug("scanner.skip", "scanner", name, "reason", reason)
		return
	}
	if l.warned == nil {
		l.warned = map[string]bool{}
	}
	l.warned[key] = true
	env.Log.Warn("scanner.skip", "scanner", name, "reason", reason, "error", err.Error())
}

func (l *Loop) run(ctx context.Context, env controller.Env, cfg *config.Resolved, d dueScanner, now time.Time) (string, error) {
	if d.builtin != nil {
		return OutcomeOK, l.runBuiltin(ctx, env, d.builtin)
	}
	p := *l.p // per-tick copy so the run logs under the loop's tick tag
	p.Log = env.Log
	since := d.due
	if since.IsZero() {
		since = now
	}
	res, err := p.Run(ctx, cfg, d.role, since)
	return res.Outcome, err
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
