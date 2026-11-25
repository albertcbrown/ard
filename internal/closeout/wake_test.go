package closeout

import (
	"context"
	"reflect"
	"sync"
	"testing"
)

func TestWakeTargets(t *testing.T) {
	l := func(n string) Label { return Label{1, n} }
	tests := []struct {
		name string
		p    Plan
		want []string
	}{
		{"worker requested", Plan{Outcome: OutcomeDone, Terminal: []Label{l("ard:triage:lead:done"), l("ard:worker:engineer")}}, []string{"triage", "workers"}},
		{"worker done requests reviewers", Plan{Outcome: OutcomeDone, Requests: []Label{l("ard:review:a"), l("ard:review:b")}, Terminal: []Label{l("ard:worker:engineer:done")}}, []string{"reviewers"}},
		{"lead requests triage", Plan{Outcome: OutcomeDone, Requests: []Label{l("ard:triage:architect")}, Terminal: []Label{l("ard:triage:lead:done")}}, []string{"triage"}},
		{"pending", Plan{Outcome: OutcomeDone, Terminal: []Label{l("ard:pending")}}, []string{"triage"}},
		{"engineer done, fan-in", Plan{Outcome: OutcomeDone, Terminal: []Label{l("ard:triage:engineer:done")}}, []string{"triage"}},
		{"review passed", Plan{Outcome: OutcomeDone, Terminal: []Label{l("ard:review:a:done")}}, []string{"merger"}},
		{"review changes", Plan{Outcome: OutcomeChanges, Terminal: []Label{l("ard:review:a:changes")}}, []string{"reviewfix", "workers"}},
		{"worker done, nothing next", Plan{Outcome: OutcomeDone, Terminal: []Label{l("ard:worker:engineer:done")}}, nil},
		{"needs human", Plan{Outcome: OutcomeNeedsHuman, Terminal: []Label{l("ard:needs-human")}}, nil},
		{"failed", Plan{Outcome: OutcomeFailed, Terminal: []Label{l("ard:executor:failed")}}, nil},
		{"retry keeps request", Plan{Outcome: OutcomeRetry, Requests: []Label{l("ard:worker:engineer")}}, nil},
		{"stale", Plan{Outcome: OutcomeStale}, nil},
		{"needs-network bounce wakes triage", Plan{Outcome: OutcomeNeedsNetwork, Requests: []Label{l("ard:triage:engineer")}}, []string{"triage"}},
		{"needs-network without bounce", Plan{Outcome: OutcomeNeedsNetwork, Terminal: []Label{l("ard:needs-human")}}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := WakeTargets(tc.p); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("WakeTargets = %v, want %v", got, tc.want)
			}
		})
	}
}

type wakeLog struct {
	mu    sync.Mutex
	calls [][]string
}

func (w *wakeLog) wake(reason string, loops ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, append([]string{reason}, loops...))
}

func TestCloseoutWakesNextLoops(t *testing.T) {
	e := newEnv(t)
	j := e.lead()
	var w wakeLog
	e.d.Wake = w.wake
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"closeout", "triage"}}
	if !reflect.DeepEqual(w.calls, want) {
		t.Errorf("wakes = %v, want %v", w.calls, want)
	}
	// A second Run of the closed job wakes nothing more.
	if _, err := New(e.d).Run(context.Background(), j); err != nil || len(w.calls) != 1 {
		t.Errorf("rerun: %v, wakes %v", err, w.calls)
	}
}

func TestCloseoutReviewChangesWakes(t *testing.T) {
	e := newEnv(t)
	e.write(".ard/7/review-eng-review.md", roleFile("eng-review", "done", "verdict: changes"))
	j := e.claim(StageReview, "eng-review")
	var w wakeLog
	e.d.Wake = w.wake
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if want := [][]string{{"closeout", "reviewfix", "workers"}}; !reflect.DeepEqual(w.calls, want) {
		t.Errorf("wakes = %v, want %v", w.calls, want)
	}
}

func TestCloseoutNoWakeWhenEffectFailsThenOnResume(t *testing.T) {
	e := newEnv(t)
	j := e.lead()
	var w wakeLog
	d := e.deps(2)
	d.Wake = w.wake
	if _, err := New(d).Run(context.Background(), j); err == nil {
		t.Fatal("want an error")
	}
	if len(w.calls) != 0 {
		t.Fatalf("woke after a failed effect: %v", w.calls)
	}
	d = e.deps(0)
	d.Wake = w.wake
	if _, err := New(d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 1 {
		t.Errorf("wakes after resume = %v, want one", w.calls)
	}
}

type dryEffects struct{ Effects }

func (dryEffects) DryRun() bool { return true }

func TestCloseoutDryRunNoWake(t *testing.T) {
	e := newEnv(t)
	j := e.lead()
	var w wakeLog
	e.d.Wake = w.wake
	e.d.Effects = dryEffects{e.d.Effects}
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 0 {
		t.Errorf("dry-run woke %v", w.calls)
	}
}
