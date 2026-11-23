package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
)

const (
	backoffAfter = 3 // consecutive errors before backoff starts
	backoffCap   = 15 * time.Minute

	// configTimeout bounds one config load, which fetches from the remote.
	configTimeout = 2 * time.Minute
)

// runner drives one Loop. Ticks of one loop never overlap and a slow tick
// never queues a second one; different loops run concurrently.
type runner struct {
	c    *Controller
	loop Loop
	log  *slog.Logger

	cfg       *config.Resolved // last good config; nil until one has loaded
	cfgBroken bool             // last reload failed
	paused    PauseState       // last pause state, for transition logs
	clamped   bool
	n         int // tick counter
	errs      int // consecutive tick errors
	wake      chan struct{}
}

// jitter maps r in [0,1) to a factor in [0.9, 1.1).
func jitter(r float64) float64 { return 0.9 + 0.2*r }

// interval returns the loop's interval clamped to MinInterval.
func (r *runner) interval() time.Duration {
	if r.cfg == nil { // no config yet: retry at the floor
		return MinInterval
	}
	d := r.loop.Interval(r.cfg)
	if d < MinInterval {
		if !r.clamped {
			r.clamped = true
			r.log.Warn("loop.interval_clamped", "requested", d.String(), "using", MinInterval.String())
		}
		return MinInterval
	}
	return d
}

// run is the loop goroutine. It returns when stopping is closed.
func (r *runner) run(workCtx context.Context, stopping <-chan struct{}) {
	wait := time.Duration(r.c.rand() * 0.1 * float64(r.interval()))
	for {
		t := time.NewTimer(wait)
		select {
		case <-stopping:
			t.Stop()
			return
		case <-t.C:
		case <-r.wake:
			t.Stop()
		}
		select { // a tick must not start once shutdown has begun
		case <-stopping:
			return
		default:
		}
		wait = r.tick(workCtx, stopping)
	}
}

// tick runs one poll and returns how long to wait before the next one.
func (r *runner) tick(workCtx context.Context, stopping <-chan struct{}) time.Duration {
	r.n++
	log := r.log.With("tick", r.n)
	r.reload(workCtx, log)

	if r.cfg == nil { // never loaded: nothing can run, and claiming loops are paused
		if r.loop.Claims() {
			r.transition(log, PauseState{Paused: true, Reason: ReasonConfigInvalid})
		}
		return r.nextWait(nil)
	}

	if r.loop.Claims() {
		st := r.c.deps.Pause.Check(workCtx, r.cfg)
		if r.cfgBroken {
			st = PauseState{Paused: true, Reason: ReasonConfigInvalid}
		}
		r.transition(log, st)
		if st.Paused {
			return r.nextWait(nil)
		}
	}

	interval := r.interval()
	timeout := interval
	if tt, ok := r.loop.(TickTimeouter); ok {
		if d := tt.TickTimeout(r.cfg); d > 0 {
			timeout = max(d, time.Second)
		}
	}
	ctx, cancel := context.WithTimeout(workCtx, timeout)
	defer cancel()

	r.c.setActive(r.loop.Name(), true)
	start := time.Now()
	err := r.safeTick(ctx, Env{Log: log, Cfg: r.cfg, Inflight: r.c.inflight, Stopping: stopping})
	r.c.setActive(r.loop.Name(), false)
	log.Debug("loop.tick", "duration", time.Since(start).String())

	if err != nil {
		r.errs++
		log.Error("loop.error", "error", err, "consecutive", r.errs)
	} else {
		r.errs = 0
	}
	return r.nextWait(err)
}

// reload refreshes the config snapshot, keeping the last good one on error.
func (r *runner) reload(ctx context.Context, log *slog.Logger) {
	ctx, cancel := context.WithTimeout(ctx, configTimeout)
	defer cancel()
	cfg, err := r.c.deps.Config(ctx)
	switch {
	case err != nil:
		if !r.cfgBroken {
			log.Error("config.reload_failed", "error", err)
		}
		r.cfgBroken = true
	default:
		if r.cfgBroken {
			log.Info("config.reloaded")
		}
		r.cfgBroken = false
		r.cfg = cfg
	}
}

// transition logs pause state changes, once per change rather than per tick.
func (r *runner) transition(log *slog.Logger, st PauseState) {
	switch {
	case st.Paused && (!r.paused.Paused || r.paused.Reason != st.Reason):
		log.Info("loop.paused", "reason", string(st.Reason))
	case !st.Paused && r.paused.Paused:
		log.Info("loop.resumed")
	}
	r.paused = st
}

// safeTick turns a panic into an error carrying the stack.
func (r *runner) safeTick(ctx context.Context, env Env) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic in %s tick: %v\n%s", r.loop.Name(), p, debug.Stack())
		}
	}()
	return r.loop.Tick(ctx, env)
}

// nextWait is the interval with jitter, or the backed-off interval after
// backoffAfter consecutive errors, or at least until a rate limit resets.
func (r *runner) nextWait(err error) time.Duration {
	interval := r.interval()
	base := interval
	if r.errs >= backoffAfter {
		limit := max(interval, backoffCap)
		base = interval
		for i := 0; i < r.errs-backoffAfter+1 && base < limit; i++ {
			base = min(base*2, limit)
		}
	}
	wait := time.Duration(float64(base) * jitter(r.c.rand()))
	var rl *github.RateLimitError
	if errors.As(err, &rl) && !rl.ResetAt.IsZero() {
		wait = max(wait, time.Until(rl.ResetAt))
	}
	return wait
}
