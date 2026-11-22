package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestNewValidation(t *testing.T) {
	log, _ := newLog()
	ok := Deps{Log: log, Config: staticCfg, Pause: never}
	tests := []struct {
		name  string
		d     Deps
		loops []Loop
	}{
		{"nil log", Deps{Config: staticCfg, Pause: never}, nil},
		{"nil config", Deps{Log: log, Pause: never}, nil},
		{"nil pause", Deps{Log: log, Config: staticCfg}, nil},
		{"empty name", ok, []Loop{&testLoop{}}},
		{"duplicate name", ok, []Loop{&testLoop{name: "a"}, &testLoop{name: "a"}}},
	}
	for _, tc := range tests {
		if _, err := New(tc.d, tc.loops...); err == nil {
			t.Errorf("%s: want error", tc.name)
		}
	}
}

func TestShutdownWaitsForRunningTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		var finished bool
		l := &testLoop{name: "a", tick: func(ctx context.Context, env Env) error {
			<-env.Stopping
			time.Sleep(10 * time.Second)
			finished = true
			return nil
		}}
		stop := start(t, mustNew(t, Deps{Log: log, Rand: fixedRand(0)}, l))
		time.Sleep(time.Second) // tick is running
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		if !finished {
			t.Error("Run returned before the tick did")
		}
	})
}

func TestInflightAcceptedDuringShutdownAndRejectedAfter(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		var done bool
		var c *Controller
		l := &testLoop{name: "a", tick: func(ctx context.Context, env Env) error {
			<-env.Stopping
			return env.Inflight.Go("closeout", func(context.Context) {
				time.Sleep(30 * time.Second)
				done = true
			})
		}}
		c = mustNew(t, Deps{Log: log, Rand: fixedRand(0)}, l)
		stop := start(t, c)
		time.Sleep(time.Second)
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		if !done {
			t.Error("Run did not wait for the inflight task")
		}
		if err := c.inflight.Go("late", func(context.Context) { t.Error("ran") }); !errors.Is(err, ErrStopping) {
			t.Errorf("Go after drain = %v, want ErrStopping", err)
		}
	})
}

func TestInflightPanicRecovered(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, buf := newLog()
		in := NewInflight(context.Background(), log)
		if err := in.Go("p", func(context.Context) { panic("x") }); err != nil {
			t.Fatal(err)
		}
		<-in.Wait()
		if buf.count("inflight.panic") != 1 || len(in.Pending()) != 0 {
			t.Errorf("log:\n%s pending=%v", buf.String(), in.Pending())
		}
	})
}

func TestGraceExpiryNamesPendingWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		l := &testLoop{name: "a", tick: func(ctx context.Context, env Env) error {
			<-env.Stopping
			return env.Inflight.Go("job-watch-7", func(ctx context.Context) { <-ctx.Done() })
		}}
		stop := start(t, mustNew(t, Deps{Log: log, Grace: time.Minute, Rand: fixedRand(0)}, l))
		time.Sleep(time.Second)
		err := stop()
		if err == nil || !strings.Contains(err.Error(), "job-watch-7") || !strings.Contains(err.Error(), "grace") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestForceAbandonsGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		force := make(chan struct{})
		l := &testLoop{name: "a", tick: func(ctx context.Context, env Env) error {
			<-ctx.Done() // only workCtx cancellation frees it
			return nil
		}}
		stop := start(t, mustNew(t, Deps{Log: log, Force: force, Grace: time.Hour, Rand: fixedRand(0)}, l))
		time.Sleep(time.Second)
		begin := time.Now()
		go func() { time.Sleep(time.Second); close(force) }()
		err := stop()
		if err == nil || err.Error() != "forced shutdown" {
			t.Errorf("err = %v", err)
		}
		if time.Since(begin) > time.Minute {
			t.Errorf("force took %v", time.Since(begin))
		}
	})
}

func TestStartupHookFailureStopsEverything(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		l := &testLoop{name: "a"}
		var order []string
		c := mustNew(t, Deps{Log: log, Rand: fixedRand(0), Startup: []Hook{
			{Name: "one", Fn: func(context.Context) error { order = append(order, "one"); return nil }},
			{Name: "bad", Fn: func(context.Context) error { return errors.New("kaput") }},
			{Name: "three", Fn: func(context.Context) error { order = append(order, "three"); return nil }},
		}}, l)
		err := c.Run(context.Background())
		if err == nil || err.Error() != "startup bad: kaput" {
			t.Fatalf("err = %v", err)
		}
		time.Sleep(time.Hour)
		if l.ticks() != 0 || len(order) != 1 {
			t.Errorf("ticks=%d order=%v", l.ticks(), order)
		}
	})
}

func TestZeroLoopsWaitsForContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		stop := start(t, mustNew(t, Deps{Log: log}))
		time.Sleep(time.Hour)
		if err := stop(); err != nil {
			t.Fatal(err)
		}
	})
}
