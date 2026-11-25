package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/albertcbrown/ard/internal/config"
)

// DefaultGrace is how long shutdown waits for ticks and in-flight work.
const DefaultGrace = 2 * time.Minute

// DefaultFreedDebounce is how long the controller collects slot releases
// before waking the loops that have demand.
const DefaultFreedDebounce = 2 * time.Second

// abandonWait is how long Run waits for goroutines to notice the cancelled
// work context after the grace period expires or Force closes.
const abandonWait = 5 * time.Second

// Hook is a named startup step run before any loop starts.
type Hook struct {
	Name string
	Fn   func(ctx context.Context) error
}

// Deps are the controller's collaborators.
type Deps struct {
	Log *slog.Logger
	// Config is called before every tick for a fresh snapshot, and once at
	// startup. An error counts as paused (fail closed): claiming loops are
	// skipped, and when no config has ever loaded no loop runs until one does.
	Config func(ctx context.Context) (*config.Resolved, error)
	Pause  Pauser
	// Startup hooks run in order before any loop starts; any error aborts Run.
	Startup []Hook
	// Grace is the shutdown wait; 0 means DefaultGrace.
	Grace time.Duration
	// Force is closed on a second signal; it abandons the grace wait.
	Force <-chan struct{}
	// Wake, when non-nil, makes every loop run one tick now for each receive
	// (still behind the kill switch and the pool). Pending wakes coalesce.
	Wake <-chan struct{}
	// Freed, when non-nil, is called once after the startup hooks and returns
	// a channel that signals a freed job slot (nil means never). The loops
	// that DemandLoops reports get one tick, after FreedDebounce.
	Freed       func() <-chan struct{}
	DemandLoops func(now time.Time) []string
	// FreedDebounce is the collect window; 0 means DefaultFreedDebounce.
	FreedDebounce time.Duration
	// Rand returns a value in [0,1); nil means math/rand/v2.Float64.
	Rand func() float64
}

// Controller schedules loops.
type Controller struct {
	deps  Deps
	loops []Loop

	inflight *Inflight // set by Run

	mu     sync.Mutex
	active map[string]bool
}

// New validates deps and loop names. It returns an error for a nil Log,
// Config or Pause, or an empty or duplicate loop name.
func New(d Deps, loops ...Loop) (*Controller, error) {
	switch {
	case d.Log == nil:
		return nil, errors.New("controller: nil Log")
	case d.Config == nil:
		return nil, errors.New("controller: nil Config")
	case d.Pause == nil:
		return nil, errors.New("controller: nil Pause")
	}
	seen := map[string]bool{}
	for _, l := range loops {
		n := l.Name()
		if n == "" {
			return nil, errors.New("controller: loop with empty name")
		}
		if seen[n] {
			return nil, fmt.Errorf("controller: duplicate loop name %q", n)
		}
		seen[n] = true
	}
	return &Controller{deps: d, loops: loops, active: map[string]bool{}}, nil
}

func (c *Controller) rand() float64 {
	if c.deps.Rand != nil {
		return c.deps.Rand()
	}
	return rand.Float64()
}

func (c *Controller) setActive(name string, on bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if on {
		c.active[name] = true
	} else {
		delete(c.active, name)
	}
}

func (c *Controller) activeTicks() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for n := range c.active {
		out = append(out, "tick:"+n)
	}
	sort.Strings(out)
	return out
}

// Run runs startup hooks, then every loop, and blocks until ctx is done and
// shutdown completes. A clean drain returns nil. When the grace period ends
// or Force closes, work contexts are cancelled and Run returns an error
// naming what was abandoned.
func (c *Controller) Run(ctx context.Context) error {
	log := c.deps.Log
	for _, h := range c.deps.Startup {
		start := time.Now()
		if err := h.Fn(ctx); err != nil {
			return fmt.Errorf("startup %s: %w", h.Name, err)
		}
		log.Info("controller.hook", "name", h.Name, "duration", time.Since(start).String())
	}
	// A config that cannot be loaded does not abort startup: the controller
	// starts paused, with no config, and retries on every tick.
	lctx, cancelLoad := context.WithTimeout(ctx, configTimeout)
	cfg, err := c.deps.Config(lctx)
	cancelLoad()
	if err != nil {
		log.Error("config.initial_failed", "error", err)
		cfg = nil
	}

	// Work contexts outlive the first signal; only grace expiry or Force
	// cancels them.
	workCtx, cancelWork := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelWork()
	c.inflight = NewInflight(workCtx, log)
	stopping := make(chan struct{})

	names := make([]string, len(c.loops))
	var wg sync.WaitGroup
	var runners []*runner
	for i, l := range c.loops {
		names[i] = l.Name()
		r := &runner{c: c, loop: l, log: log.With("loop", l.Name()), cfg: cfg, cfgBroken: err != nil, wake: make(chan struct{}, 1)}
		runners = append(runners, r)
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.run(workCtx, stopping)
		}()
	}
	log.Info("controller.start", "loops", names)
	if c.deps.Wake != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopping:
					return
				case <-c.deps.Wake:
					select { // a wake racing shutdown starts nothing
					case <-stopping:
						return
					default:
					}
					log.Info("controller.wake", "loops", names)
					for _, r := range runners {
						select {
						case r.wake <- struct{}{}:
						default: // one already pending
						}
					}
				}
			}
		}()
	}

	if c.deps.Freed != nil && c.deps.DemandLoops != nil {
		if freed := c.deps.Freed(); freed != nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.wakeOnFreed(freed, runners, stopping)
			}()
		}
	}

	<-ctx.Done()
	log.Info("controller.stopping")
	close(stopping)

	grace := c.deps.Grace
	if grace == 0 {
		grace = DefaultGrace
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()

	drained := make(chan struct{})
	go func() {
		wg.Wait() // every in-progress tick has returned
		c.inflight.close()
		<-c.inflight.Wait()
		close(drained)
	}()

	var result error
	select {
	case <-drained:
	case <-timer.C:
		abandoned := append(c.activeTicks(), c.inflight.Pending()...)
		result = fmt.Errorf("shutdown grace %s exceeded; abandoned: %s", grace, strings.Join(abandoned, ", "))
		c.abandon(cancelWork, drained)
	case <-c.deps.Force:
		result = errors.New("forced shutdown")
		c.abandon(cancelWork, drained)
	}
	log.Info("controller.stopped", "clean", result == nil)
	return result
}

// abandon cancels the work context and gives goroutines a moment to return.
func (c *Controller) abandon(cancel context.CancelFunc, drained <-chan struct{}) {
	cancel()
	t := time.NewTimer(abandonWait)
	defer t.Stop()
	select {
	case <-drained:
	case <-t.C:
	}
}

// wakeOnFreed waits for slot-freed signals, debounces them, and wakes the
// runners with live demand for one tick each.
func (c *Controller) wakeOnFreed(freed <-chan struct{}, runners []*runner, stopping <-chan struct{}) {
	window := c.deps.FreedDebounce
	if window <= 0 {
		window = DefaultFreedDebounce
	}
	for {
		select {
		case <-stopping:
			return
		case <-freed:
		}
		timer := time.NewTimer(window)
		for waiting := true; waiting; {
			select {
			case <-stopping:
				timer.Stop()
				return
			case <-freed: // coalesced into this window
			case <-timer.C:
				waiting = false
			}
		}
		loops := c.deps.DemandLoops(time.Now())
		if len(loops) == 0 {
			continue
		}
		want := map[string]bool{}
		for _, n := range loops {
			want[n] = true
		}
		var woken []string
		for _, r := range runners {
			if !want[r.loop.Name()] {
				continue
			}
			woken = append(woken, r.loop.Name())
			select {
			case r.wake <- struct{}{}:
			default: // one already pending
			}
		}
		if len(woken) > 0 {
			c.deps.Log.Info("controller.wake", "reason", "slot_freed", "loops", woken)
		}
	}
}
