package reaper

import (
	"context"
	"reflect"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/state"
)

type captureCloser struct{ got []closeout.Job }

func (c *captureCloser) Run(_ context.Context, j closeout.Job) (closeout.Outcome, error) {
	c.got = append(c.got, j)
	return closeout.OutcomeDone, nil
}

func (c *captureCloser) ResumeJob(context.Context, string) (closeout.Outcome, error) {
	return "", nil
}

func TestFinishWithPassesWorkerResult(t *testing.T) {
	ctx := context.Background()
	st, err := state.Open(ctx, t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sj, err := st.CreateJob(ctx, state.NewJob{Stage: "worker", Role: "engineer", Issue: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetBranch(ctx, sj.ID, "worker/engineer-4-x"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPR(ctx, sj.ID, 12); err != nil {
		t.Fatal(err)
	}
	cc := &captureCloser{}
	f := &Finisher{Closer: cc, Store: st, Log: quiet}
	w := &closeout.WorkerResult{PR: 12, Tests: closeout.TestsPassed, Reviewers: []string{"eng-review"}}
	extra := []string{"/logs/a.log"}
	h := executor.Handle{JobID: sj.ID, LogPath: "/logs/b.log", BaseSHA: "abc"}
	if _, err := f.FinishWith(ctx, sj.ID, h, executor.Status{State: executor.Exited}, agent.Result{}, FinishOptions{Worker: w, ExtraLogs: extra}); err != nil {
		t.Fatal(err)
	}
	if len(cc.got) != 1 {
		t.Fatalf("Run calls = %d", len(cc.got))
	}
	j := cc.got[0]
	if j.Worker != w || !reflect.DeepEqual(j.ExtraLogs, extra) || j.PR != 12 || j.Branch != "worker/engineer-4-x" || j.LogPath != "/logs/b.log" {
		t.Errorf("closeout job = %+v", j)
	}
}

func TestFinishWithPassesReviewContext(t *testing.T) {
	ctx := context.Background()
	st, err := state.Open(ctx, t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sj, _ := st.CreateJob(ctx, state.NewJob{Stage: "review", Role: "eng-review", Issue: 4, PR: 12, Branch: "worker/engineer-4-x"})
	cc := &captureCloser{}
	f := &Finisher{Closer: cc, Store: st, Log: quiet}
	rc := &closeout.ReviewContext{MergeBase: "mb", HeadMoved: true}
	if _, err := f.FinishWith(ctx, sj.ID, executor.Handle{JobID: sj.ID, BaseSHA: "abc"}, executor.Status{State: executor.Exited}, agent.Result{}, FinishOptions{Review: rc}); err != nil {
		t.Fatal(err)
	}
	if j := cc.got[0]; j.Review != rc || j.PR != 12 || j.Stage != "review" {
		t.Errorf("closeout job = %+v", j)
	}
}

func TestFinishLeavesWorkerFieldsEmpty(t *testing.T) {
	ctx := context.Background()
	st, err := state.Open(ctx, t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sj, _ := st.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "lead", Issue: 4})
	cc := &captureCloser{}
	f := &Finisher{Closer: cc, Store: st, Log: quiet}
	if _, err := f.Finish(ctx, sj.ID, executor.Handle{JobID: sj.ID}, executor.Status{State: executor.Exited}, agent.Result{}, closeout.Cause{}); err != nil {
		t.Fatal(err)
	}
	if j := cc.got[0]; j.Worker != nil || j.ExtraLogs != nil || j.Review != nil {
		t.Errorf("closeout job = %+v", j)
	}
}
