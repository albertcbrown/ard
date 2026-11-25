package controller

import (
	"context"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/albertcbrown/ard/internal/config"
)

type demandFn struct {
	mu    sync.Mutex
	loops []string
}

func (d *demandFn) get(time.Time) []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.loops
}

func freedController(t *testing.T, freed chan struct{}, d *demandFn, pause Pauser, ls ...Loop) (func() error, *logBuf) {
	log, buf := newLog()
	deps := Deps{
		Log: log, Rand: fixedRand(0.5),
		Freed:       func() <-chan struct{} { return freed },
		DemandLoops: d.get,
	}
	if pause != nil {
		deps.Pause = pause
	}
	return start(t, mustNew(t, deps, ls...)), buf
}

func TestFreedWakesDemandLoopOnly(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := make(chan struct{}, 1)
		a := &testLoop{name: "a", claims: true}
		b := &testLoop{name: "b", claims: true}
		stop, buf := freedController(t, freed, &demandFn{loops: []string{"a"}}, nil, a, b)
		time.Sleep(iv / 2)
		freed <- struct{}{}
		time.Sleep(DefaultFreedDebounce + time.Second)
		if a.ticks() != 2 || b.ticks() != 1 {
			t.Errorf("ticks a=%d b=%d, want 2 and 1", a.ticks(), b.ticks())
		}
		_ = stop()
		if buf.count("reason=slot_freed") != 1 {
			t.Errorf("want one slot_freed line:\n%s", buf.String())
		}
	})
}

func TestFreedDebounceCoalesces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := make(chan struct{}, 1)
		a := &testLoop{name: "a", claims: true}
		stop, buf := freedController(t, freed, &demandFn{loops: []string{"a"}}, nil, a)
		time.Sleep(iv / 2)
		for range 3 {
			freed <- struct{}{}
			time.Sleep(500 * time.Millisecond)
		}
		time.Sleep(DefaultFreedDebounce + time.Second)
		if a.ticks() != 2 {
			t.Errorf("ticks = %d, want 2", a.ticks())
		}
		_ = stop()
		if buf.count("reason=slot_freed") != 1 {
			t.Errorf("want one wake line:\n%s", buf.String())
		}
	})
}

func TestFreedNoDemandNoWake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := make(chan struct{}, 1)
		a := &testLoop{name: "a", claims: true}
		stop, buf := freedController(t, freed, &demandFn{}, nil, a)
		time.Sleep(iv / 2)
		freed <- struct{}{}
		time.Sleep(DefaultFreedDebounce + time.Second)
		if a.ticks() != 1 {
			t.Errorf("ticks = %d, want 1", a.ticks())
		}
		_ = stop()
		if buf.count("slot_freed") != 0 {
			t.Errorf("unexpected wake log:\n%s", buf.String())
		}
	})
}

func TestFreedWhilePausedRunsNoTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := make(chan struct{}, 1)
		a := &testLoop{name: "a", claims: true}
		paused := pauseFunc(func() PauseState { return PauseState{Paused: true, Reason: ReasonConfigInvalid} })
		stop, _ := freedController(t, freed, &demandFn{loops: []string{"a"}}, paused, a)
		time.Sleep(iv / 2)
		freed <- struct{}{}
		time.Sleep(DefaultFreedDebounce + time.Second)
		_ = stop()
		if a.ticks() != 0 {
			t.Errorf("ticks = %d, want 0", a.ticks())
		}
	})
}

func TestFreedWakeDuringTickRunsOneMore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := make(chan struct{}, 1)
		a := &testLoop{name: "a", tick: func(ctx context.Context, _ Env) error {
			time.Sleep(time.Minute)
			return nil
		}, interval: func(*config.Resolved) time.Duration { return time.Hour }}
		stop, _ := freedController(t, freed, &demandFn{loops: []string{"a"}}, nil, a)
		time.Sleep(time.Second) // first tick running
		for range 3 {
			freed <- struct{}{}
			time.Sleep(3 * time.Second)
		}
		time.Sleep(10 * time.Minute)
		_ = stop()
		if a.maxCur != 1 {
			t.Errorf("max concurrent = %d", a.maxCur)
		}
		if got := a.ticks(); got != 2 {
			t.Errorf("ticks = %d, want 2", got)
		}
	})
}

func TestFreedDuringShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		freed := make(chan struct{}, 1)
		a := &testLoop{name: "a", claims: true}
		stop, _ := freedController(t, freed, &demandFn{loops: []string{"a"}}, nil, a)
		time.Sleep(iv / 2)
		freed <- struct{}{}
		_ = stop()
		time.Sleep(time.Minute)
		if a.ticks() != 1 {
			t.Errorf("ticks = %d, want 1", a.ticks())
		}
	})
}
