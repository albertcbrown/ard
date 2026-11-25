package controller

import (
	"context"
	"log/slog"
	"testing"
	"testing/synctest"
	"time"

	"github.com/albertcbrown/ard/internal/config"
)

func wakeController(t *testing.T, pause Pauser, ls ...Loop) (*Controller, func() error, *logBuf) {
	log, buf := newLog()
	d := Deps{Log: log, Rand: fixedRand(0.5)}
	if pause != nil {
		d.Pause = pause
	}
	c := mustNew(t, d, ls...)
	return c, start(t, c), buf
}

func TestWakeLoopsWakesOnlyNamed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &testLoop{name: "a", claims: true}
		b := &testLoop{name: "b", claims: true}
		c, stop, buf := wakeController(t, nil, a, b)
		time.Sleep(iv / 2)
		c.WakeLoops(WakeCloseout, "a", "nope")
		time.Sleep(DefaultFreedDebounce + time.Second)
		if a.ticks() != 2 || b.ticks() != 1 {
			t.Errorf("ticks a=%d b=%d, want 2 and 1", a.ticks(), b.ticks())
		}
		_ = stop()
		if buf.count("reason=closeout") != 1 || buf.count("loops=[a]") != 1 {
			t.Errorf("log:\n%s", buf.String())
		}
	})
}

func TestWakeLoopsBurstCoalesces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &testLoop{name: "a", claims: true}
		c, stop, buf := wakeController(t, nil, a)
		time.Sleep(iv / 2)
		for range 10 {
			c.WakeLoops(WakeCloseout, "a")
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(DefaultFreedDebounce + time.Second)
		if a.ticks() != 2 {
			t.Errorf("ticks = %d, want 2", a.ticks())
		}
		_ = stop()
		if buf.count("reason=closeout") != 1 {
			t.Errorf("want one wake line:\n%s", buf.String())
		}
	})
}

func TestWakeLoopsDuringTickRunsOneMore(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &testLoop{name: "a", tick: func(context.Context, Env) error {
			time.Sleep(time.Minute)
			return nil
		}, interval: func(*config.Resolved) time.Duration { return time.Hour }}
		c, stop, _ := wakeController(t, nil, a)
		time.Sleep(time.Second)
		for range 3 {
			c.WakeLoops(WakeApproval, "a")
			time.Sleep(3 * time.Second)
		}
		time.Sleep(10 * time.Minute)
		_ = stop()
		if a.maxCur != 1 || a.ticks() != 2 {
			t.Errorf("max concurrent %d, ticks %d; want 1 and 2", a.maxCur, a.ticks())
		}
	})
}

func TestWakeLoopsWhilePausedRunsNoTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &testLoop{name: "a", claims: true}
		paused := pauseFunc(func() PauseState { return PauseState{Paused: true, Reason: ReasonConfigInvalid} })
		c, stop, _ := wakeController(t, paused, a)
		time.Sleep(iv / 2)
		c.WakeLoops(WakeCloseout, "a")
		time.Sleep(DefaultFreedDebounce + time.Second)
		_ = stop()
		if a.ticks() != 0 {
			t.Errorf("ticks = %d, want 0", a.ticks())
		}
	})
}

func TestWakeLoopsBeforeRunAndAfterShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := &testLoop{name: "a", claims: true}
		c, stop, _ := wakeController(t, nil, a)
		time.Sleep(iv / 2)
		c.WakeLoops(WakeCloseout, "a") // pending when shutdown starts
		_ = stop()
		c.WakeLoops(WakeCloseout, "a")
		time.Sleep(time.Minute)
		if a.ticks() != 1 {
			t.Errorf("ticks = %d, want 1", a.ticks())
		}
		fresh := mustNew(t, Deps{Log: slog.Default()}, a)
		fresh.WakeLoops(WakeCloseout, "a") // not running yet: ignored, no panic
	})
}
