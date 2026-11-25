package controller

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
)

type wakeRec struct {
	mu   sync.Mutex
	got  []string
	call int
}

func (w *wakeRec) wake(reason string, names ...string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.got = append(w.got, reason+":"+join(names))
	w.call++
}

func join(ns []string) string {
	s := ""
	for i, n := range ns {
		if i > 0 {
			s += ","
		}
		s += n
	}
	return s
}

// fakeSearch answers by label named in the query.
type fakeSearch struct {
	approved, needsHuman, failed []github.Issue
	err                          error
	queries                      []string
}

func (f *fakeSearch) search(_ context.Context, q string, _ github.SearchOpts) ([]github.Issue, error) {
	f.queries = append(f.queries, q)
	if f.err != nil {
		return nil, f.err
	}
	switch q {
	case `is:open label:"ard:approved"`:
		return f.approved, nil
	case `is:open label:"ard:needs-human"`:
		return f.needsHuman, nil
	case `is:open label:"ard:executor:failed"`:
		return f.failed, nil
	}
	return nil, errors.New("unexpected query " + q)
}

func watchTick(t *testing.T, w *ApprovalWatch) error {
	t.Helper()
	log, _ := newLog()
	return w.Tick(context.Background(), Env{Log: log, Cfg: &config.Resolved{}})
}

func TestApprovalWatchApprovedWakesTriageOnce(t *testing.T) {
	fs := &fakeSearch{approved: []github.Issue{
		{Number: 1, Labels: []string{"ard:approved", "ard:triage:engineer:done"}, UpdatedAt: time.Unix(10, 0)},
		{Number: 2, Labels: []string{"ard:approved", "ard:worker:engineer"}}, // already has a worker label
	}}
	rec := &wakeRec{}
	w := &ApprovalWatch{Search: fs.search, Wake: rec.wake}
	for range 2 { // second poll: same item, same UpdatedAt: no second wake
		if err := watchTick(t, w); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"approval:triage"}; !reflect.DeepEqual(rec.got, want) {
		t.Errorf("wakes = %v, want %v", rec.got, want)
	}
	fs.approved[0].UpdatedAt = time.Unix(20, 0) // re-approved
	_ = watchTick(t, w)
	if rec.call != 2 {
		t.Errorf("wakes = %v, want a second after the update", rec.got)
	}
}

func TestApprovalWatchNoHitNoWake(t *testing.T) {
	rec := &wakeRec{}
	w := &ApprovalWatch{Search: (&fakeSearch{}).search, Wake: rec.wake}
	_ = watchTick(t, w)
	_ = watchTick(t, w)
	if rec.call != 0 {
		t.Errorf("wakes = %v", rec.got)
	}
}

func TestApprovalWatchUnblockedWakesOwner(t *testing.T) {
	fs := &fakeSearch{
		needsHuman: []github.Issue{{Number: 5, Labels: []string{"ard:needs-human", "ard:worker:engineer:running"}}},
		failed:     []github.Issue{{Number: 6, IsPR: true, Labels: []string{"ard:executor:failed", "ard:review:sec:changes"}}},
	}
	rec := &wakeRec{}
	w := &ApprovalWatch{Search: fs.search, Wake: rec.wake}
	_ = watchTick(t, w) // first poll: baseline only
	if rec.call != 0 {
		t.Fatalf("baseline woke: %v", rec.got)
	}
	fs.needsHuman, fs.failed = nil, nil
	_ = watchTick(t, w)
	if len(rec.got) != 1 || rec.got[0] != "unblocked:workers,reviewers,reviewfix" && rec.got[0] != "unblocked:reviewers,reviewfix,workers" {
		t.Errorf("wakes = %v", rec.got)
	}
	_ = watchTick(t, w)
	if rec.call != 1 {
		t.Errorf("repeat wake: %v", rec.got)
	}
}

func TestApprovalWatchPausedDoesNothing(t *testing.T) {
	fs := &fakeSearch{approved: []github.Issue{{Number: 1, Labels: []string{"ard:approved"}}}}
	rec := &wakeRec{}
	w := &ApprovalWatch{Search: fs.search, Wake: rec.wake, Paused: func(context.Context) bool { return true }}
	_ = watchTick(t, w)
	if rec.call != 0 || len(fs.queries) != 0 {
		t.Errorf("paused watch searched %v and woke %v", fs.queries, rec.got)
	}
}

func TestApprovalWatchSearchErrorThenRecovers(t *testing.T) {
	fs := &fakeSearch{err: errors.New("boom"), approved: []github.Issue{{Number: 1, Labels: []string{"ard:approved"}}}}
	rec := &wakeRec{}
	w := &ApprovalWatch{Search: fs.search, Wake: rec.wake}
	if err := watchTick(t, w); err == nil {
		t.Fatal("want the search error")
	}
	fs.err = nil
	if err := watchTick(t, w); err != nil || rec.call != 1 {
		t.Errorf("err %v wakes %v", err, rec.got)
	}
}

func TestApprovalWatchLoopContract(t *testing.T) {
	w := &ApprovalWatch{}
	cfg := &config.Resolved{Intervals: config.Intervals{Approvals: config.Duration(time.Minute)}}
	if w.Name() != "approvals" || w.Claims() || w.Interval(cfg) != time.Minute {
		t.Error("loop contract")
	}
}
