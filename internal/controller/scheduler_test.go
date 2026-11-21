package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
)

const iv = 5 * time.Minute

func TestFirstAndLaterTicks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		l := &testLoop{name: "a", claims: true}
		begin := time.Now()
		stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0.5)}, l))
		time.Sleep(iv * 3)
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		if got := l.starts[0].Sub(begin); got != iv/20 { // 0.5 * 10% of the interval
			t.Errorf("first tick after %v, want %v", got, iv/20)
		}
		for _, g := range l.gaps() {
			if g != iv { // jitter factor 0.9+0.2*0.5 = 1
				t.Errorf("gap %v, want %v", g, iv)
			}
		}
		if l.ticks() < 3 {
			t.Errorf("ticks = %d, want >= 3", l.ticks())
		}
	})
}

func TestJitterBounds(t *testing.T) {
	for _, r := range []float64{0, 0.25, 0.999} {
		synctest.Test(t, func(t *testing.T) {
			log, _ := newLog()
			l := &testLoop{name: "a"}
			begin := time.Now()
			stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(r)}, l))
			time.Sleep(iv * 4)
			_ = stop()
			if f := l.starts[0].Sub(begin); f < 0 || f > iv/10 {
				t.Errorf("r=%v: first tick %v outside [0, 10%%]", r, f)
			}
			for _, g := range l.gaps() {
				if g < iv*9/10 || g > iv*11/10 {
					t.Errorf("r=%v: gap %v outside [0.9, 1.1]*interval", r, g)
				}
			}
		})
	}
}

func TestNoOverlapWhenTickIsSlow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		l := &testLoop{name: "slow", tick: func(ctx context.Context, _ Env) error {
			time.Sleep(2 * iv)
			return nil
		}, interval: func(*config.Resolved) time.Duration { return iv }}
		// tick timeout defaults to the interval, so the sleep is cut short
		// only if the loop honours ctx; here it does not, which is the point.
		stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0.5)}, l))
		time.Sleep(iv * 20)
		_ = stop()
		if l.maxCur != 1 {
			t.Errorf("max concurrent ticks = %d, want 1", l.maxCur)
		}
		if l.ticks() < 3 {
			t.Errorf("ticks = %d", l.ticks())
		}
	})
}

func TestPanicIsRecovered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, buf := newLog()
		var n atomic.Int32
		l := &testLoop{name: "p", tick: func(context.Context, Env) error {
			if n.Add(1) == 1 {
				panic("boom")
			}
			return nil
		}}
		stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0.5)}, l))
		time.Sleep(iv * 3)
		_ = stop()
		if l.ticks() < 2 {
			t.Fatalf("ticks = %d, want the loop to survive the panic", l.ticks())
		}
		if buf.count("loop.error") != 1 || buf.count("boom") == 0 {
			t.Errorf("log:\n%s", buf.String())
		}
	})
}

func TestBackoff(t *testing.T) {
	tests := []struct {
		name     string
		interval time.Duration
		want     []time.Duration // gap after the k-th error, k = 1..
	}{
		{"5m", 5 * time.Minute, []time.Duration{5, 5, 10, 15, 15, 15}},
		{"4h never exceeds its interval", 4 * time.Hour, []time.Duration{240, 240, 240, 240}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				log, _ := newLog()
				l := &testLoop{name: "e", interval: func(*config.Resolved) time.Duration { return tc.interval },
					tick: func(context.Context, Env) error { return errors.New("nope") }}
				stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0.5)}, l))
				time.Sleep(tc.interval * 40)
				_ = stop()
				gaps := l.gaps()
				for i, w := range tc.want {
					if i >= len(gaps) {
						t.Fatalf("only %d gaps, want %d", len(gaps), len(tc.want))
					}
					if want := w * time.Minute; gaps[i] != want {
						t.Errorf("gap after error %d = %v, want %v", i+1, gaps[i], want)
					}
				}
			})
		})
	}
}

func TestBackoffRespectsJitterBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		l := &testLoop{name: "e", interval: func(*config.Resolved) time.Duration { return 4 * time.Hour },
			tick: func(context.Context, Env) error { return errors.New("nope") }}
		stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0.999)}, l))
		time.Sleep(100 * time.Hour)
		_ = stop()
		for _, g := range l.gaps() {
			if g > 4*time.Hour*11/10 {
				t.Errorf("gap %v exceeds 1.1 * interval", g)
			}
		}
	})
}

func TestRateLimitDelaysUntilReset(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		var n atomic.Int32
		l := &testLoop{name: "r", tick: func(context.Context, Env) error {
			if n.Add(1) == 1 {
				return &github.RateLimitError{ResetAt: time.Now().Add(time.Hour)}
			}
			return nil
		}}
		stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0.5)}, l))
		time.Sleep(3 * time.Hour)
		_ = stop()
		if g := l.gaps()[0]; g < time.Hour {
			t.Errorf("gap after rate limit = %v, want >= 1h", g)
		}
	})
}

func TestIntervalClamped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, buf := newLog()
		l := &testLoop{name: "c", interval: func(*config.Resolved) time.Duration { return 10 * time.Second }}
		stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0.5)}, l))
		time.Sleep(10 * time.Minute)
		_ = stop()
		for _, g := range l.gaps() {
			if g != MinInterval {
				t.Fatalf("gap %v, want %v", g, MinInterval)
			}
		}
		if buf.count("loop.interval_clamped") != 1 {
			t.Errorf("clamp warning count = %d, want 1", buf.count("loop.interval_clamped"))
		}
	})
}

func TestConfigReloadFailureFailsClosed(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, buf := newLog()
		var broken atomic.Bool
		cfgFn := func(context.Context) (*config.Resolved, error) {
			if broken.Load() {
				return nil, errors.New("bad yaml")
			}
			return &config.Resolved{}, nil
		}
		claim := &testLoop{name: "claim", claims: true}
		reap := &testLoop{name: "reaper"}
		stop := start(t, mustNew(t, Deps{Log: log, Config: cfgFn, Rand: fixedRand(0.5)}, claim, reap))
		time.Sleep(iv * 3)
		broken.Store(true)
		c0, r0 := claim.ticks(), reap.ticks()
		time.Sleep(iv * 5)
		if claim.ticks() > c0+1 { // one tick may already be in flight
			t.Errorf("claiming loop ticked %d times with invalid config", claim.ticks()-c0)
		}
		if reap.ticks() <= r0+2 {
			t.Errorf("non-claiming loop stalled: %d -> %d", r0, reap.ticks())
		}
		broken.Store(false)
		c1 := claim.ticks()
		time.Sleep(iv * 3)
		_ = stop()
		if claim.ticks() == c1 {
			t.Error("claiming loop did not resume after config recovered")
		}
		if buf.count("loop.paused") != 1 || buf.count("reason=config-invalid") != 1 || buf.count("loop.resumed") != 1 {
			t.Errorf("transition logs wrong:\n%s", buf.String())
		}
		if n := buf.count("config.reload_failed"); n != 2 { // once per loop, not once per tick
			t.Errorf("reload_failed logged %d times, want 2", n)
		}
	})
}

func TestIntervalChangeAppliesOnNextWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		var fast atomic.Bool
		cfgFn := func(context.Context) (*config.Resolved, error) {
			r := &config.Resolved{}
			if fast.Load() {
				r.Intervals.Workers = config.Duration(2 * time.Minute)
			} else {
				r.Intervals.Workers = config.Duration(10 * time.Minute)
			}
			return r, nil
		}
		l := &testLoop{name: "w", interval: func(c *config.Resolved) time.Duration {
			return time.Duration(c.Intervals.Workers)
		}}
		stop := start(t, mustNew(t, Deps{Log: log, Config: cfgFn, Rand: fixedRand(0.5)}, l))
		time.Sleep(25 * time.Minute)
		fast.Store(true)
		time.Sleep(30 * time.Minute)
		_ = stop()
		gaps := l.gaps()
		if gaps[0] != 10*time.Minute || gaps[len(gaps)-1] != 2*time.Minute {
			t.Errorf("gaps = %v; want 10m first and 2m last", gaps)
		}
	})
}

func TestTickTimeoutOverride(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		var got atomic.Int64
		l := &timeoutLoop{testLoop: testLoop{name: "t", tick: func(ctx context.Context, _ Env) error {
			d, _ := ctx.Deadline()
			got.Store(int64(time.Until(d)))
			return nil
		}}}
		stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0)}, l))
		time.Sleep(time.Minute)
		_ = stop()
		if d := time.Duration(got.Load()); d != 90*time.Second {
			t.Errorf("tick deadline in %v, want 90s", d)
		}
		def := &testLoop{name: "d", tick: func(ctx context.Context, _ Env) error {
			d, _ := ctx.Deadline()
			got.Store(int64(time.Until(d)))
			return nil
		}}
		stop = start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0)}, def))
		time.Sleep(time.Minute)
		_ = stop()
		if d := time.Duration(got.Load()); d != iv {
			t.Errorf("default tick deadline in %v, want the interval %v", d, iv)
		}
	})
}

type timeoutLoop struct{ testLoop }

func (*timeoutLoop) TickTimeout(*config.Resolved) time.Duration { return 90 * time.Second }
