package controller

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestPauseStatesAndTicksRecorded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		log, _ := newLog()
		var checks atomic.Int32
		pause := pauseFunc(func() PauseState {
			checks.Add(1)
			return PauseState{Paused: true, Reason: ReasonLabel}
		})
		claiming := &testLoop{name: "claiming", claims: true}
		plain := &testLoop{name: "plain", tick: func(_ context.Context, _ Env) error { return errors.New("boom") }}
		c := mustNew(t, Deps{Log: log, Pause: pause, Rand: fixedRand(0.5)}, claiming, plain)
		begin := time.Now()
		stop := start(t, c)
		time.Sleep(iv)
		got := checks.Load()
		ps := c.PauseStates()
		tk := c.Ticks()
		_ = c.PauseStates() // reads never check the gate
		if checks.Load() != got {
			t.Errorf("PauseStates called the gate")
		}
		if err := stop(); err != nil {
			t.Fatal(err)
		}
		if st := ps["claiming"]; !st.Paused || st.Reason != ReasonLabel {
			t.Errorf("claiming pause = %+v", st)
		}
		if _, ok := ps["plain"]; ok {
			t.Errorf("non-claiming loop has a pause state: %+v", ps)
		}
		if got < 1 || claiming.ticks() != 0 {
			t.Errorf("checks=%d claimingTicks=%d", got, claiming.ticks())
		}
		if len(tk) != 2 || tk[0].Loop != "claiming" || tk[1].Loop != "plain" {
			t.Fatalf("ticks = %+v", tk)
		}
		if tk[0].Count < 1 || tk[0].Start.Before(begin) || !tk[0].End.IsZero() {
			t.Errorf("claiming tick (paused, never ran) = %+v", tk[0])
		}
		if tk[1].Err != "boom" || tk[1].End.Before(tk[1].Start) || tk[1].End.IsZero() {
			t.Errorf("plain tick = %+v", tk[1])
		}
	})
}
