package reaper

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/agent"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/executor"
	"github.com/albertcbrown/ard/internal/state"
)

func usageResult(cost float64, in, out, cw, cr int64, model string) agent.Result {
	u := agent.Usage{Input: in, Output: out, CacheWrite: cw, CacheRead: cr}
	return agent.Result{OK: true, CostUSD: &cost, Usage: &u, Model: model, ModelUsage: map[string]agent.Usage{model: u}}
}

func newUsageStore(t *testing.T) (*state.Store, context.Context) {
	t.Helper()
	ctx := context.Background()
	st, err := state.Open(ctx, t.TempDir(), state.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, ctx
}

func TestStepUsageOf(t *testing.T) {
	if _, ok := StepUsageOf(agent.Result{}); ok {
		t.Error("an empty result reported usage")
	}
	res := usageResult(0.4, 1, 2, 3, 4, "claude-opus-5-5")
	res.ModelUsage["claude-haiku-4-5"] = agent.Usage{Input: 1}
	res.ModelUsage["bad model"] = agent.Usage{}
	u, ok := StepUsageOf(res)
	if !ok || *u.CostUSD != 0.4 || *u.Tokens != (state.Tokens{Input: 1, Output: 2, CacheWrite: 3, CacheRead: 4}) ||
		strings.Join(u.Models, ",") != "claude-haiku-4-5,claude-opus-5-5" {
		t.Errorf("usage = %+v ok=%v", u, ok)
	}
}

func TestFinishWithRecordsUsage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cause   closeout.Cause
		stateOK bool
	}{
		{"none", closeout.Cause{}, true},
		{"rate_limit", closeout.Cause{Kind: closeout.CauseRateLimit, Transient: true, Retry: true}, true},
		{"usage_limit", closeout.Cause{Kind: closeout.CauseUsageLimit}, true},
		{"deadline", closeout.Cause{Kind: closeout.CauseDeadline}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, ctx := newUsageStore(t)
			sj, _ := st.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "lead", Issue: 4})
			if _, err := st.StartAttempt(ctx, sj.ID); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			cc := &captureCloser{}
			f := &Finisher{Closer: cc, Store: st, Log: slog.New(slog.NewTextHandler(&logs, nil))}
			res := usageResult(0.42, 20, 3300, 19100, 173500, "claude-sonnet-5-5")
			if _, err := f.Finish(ctx, sj.ID, executor.Handle{JobID: sj.ID}, executor.Status{State: executor.Exited}, res, tc.cause); err != nil {
				t.Fatal(err)
			}
			j := cc.got[0]
			if j.Usage.Steps != 1 || j.Usage.Tokens == nil || *j.Usage.Tokens != (closeout.Tokens{Input: 20, Output: 3300, CacheWrite: 19100, CacheRead: 173500}) ||
				*j.Usage.CostUSD != 0.42 || j.CostUSD != 0.42 || strings.Join(j.Usage.Models, ",") != "claude-sonnet-5-5" {
				t.Errorf("closeout usage = %+v cost=%v", j.Usage, j.CostUSD)
			}
			if j.StartedAt.IsZero() {
				t.Error("StartedAt not set")
			}
			at, _ := st.Attempts(ctx, sj.ID)
			if len(at) != 1 || at[0].CostUSD == nil || *at[0].CostUSD != 0.42 {
				t.Errorf("attempts = %+v", at)
			}
			if got, _ := st.Job(ctx, sj.ID); got.CostUSD == nil || *got.CostUSD != 0.42 {
				t.Errorf("jobs.cost_usd = %v", got.CostUSD)
			}
			for _, want := range []string{"tokens_in=20", "tokens_out=3300", "tokens_cache_write=19100", "tokens_cache_read=173500", "cost_usd=0.42", "steps=1"} {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("reaper.finish log lacks %s: %s", want, logs.String())
				}
			}
		})
	}
}

func TestFinishWithEmptyResultRecordsNothing(t *testing.T) {
	st, ctx := newUsageStore(t)
	sj, _ := st.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "lead", Issue: 4})
	cc := &captureCloser{}
	f := &Finisher{Closer: cc, Store: st, Log: quiet}
	if _, err := f.Finish(ctx, sj.ID, executor.Handle{JobID: sj.ID}, executor.Status{State: executor.Exited}, agent.Result{}, closeout.Cause{}); err != nil {
		t.Fatal(err)
	}
	u, _ := st.JobUsage(ctx, sj.ID)
	if u.Steps != 0 || cc.got[0].Usage.CostUSD != nil || cc.got[0].CostUSD != 0 || cc.got[0].Usage.Tokens != nil {
		t.Errorf("usage = %+v closeout = %+v", u, cc.got[0].Usage)
	}
}

type failingRecord struct{ *state.Store }

func (failingRecord) RecordStepUsage(context.Context, string, string, state.StepUsage) (bool, error) {
	return false, errors.New("disk full")
}

func TestFinishWithRecordErrorIsLoggedNotReturned(t *testing.T) {
	st, ctx := newUsageStore(t)
	sj, _ := st.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "lead", Issue: 4})
	var logs bytes.Buffer
	cc := &captureCloser{}
	f := &Finisher{Closer: cc, Store: failingRecord{st}, Log: slog.New(slog.NewTextHandler(&logs, nil))}
	out, err := f.Finish(ctx, sj.ID, executor.Handle{JobID: sj.ID}, executor.Status{State: executor.Exited}, usageResult(0.5, 1, 2, 3, 4, "m"), closeout.Cause{})
	if err != nil || out != closeout.OutcomeDone {
		t.Fatalf("Finish = %s, %v", out, err)
	}
	if !strings.Contains(logs.String(), "reaper.usage_record_failed") {
		t.Errorf("no log line: %s", logs.String())
	}
	// The final step's own numbers stand in.
	if j := cc.got[0]; j.CostUSD != 0.5 || j.Usage.Tokens == nil || j.Usage.Tokens.Output != 2 {
		t.Errorf("closeout = %+v", j)
	}
}

// Steps recorded earlier by the worker are not counted again when the job is
// finished with the handle of its last step.
func TestFinishWithDoesNotDoubleCount(t *testing.T) {
	st, ctx := newUsageStore(t)
	sj, _ := st.CreateJob(ctx, state.NewJob{Stage: "worker", Role: "engineer", Issue: 4})
	for step, r := range map[string]agent.Result{
		sj.ID:        usageResult(1, 10, 10, 10, 10, "claude-opus-5-5"),
		sj.ID + "f1": usageResult(2, 20, 20, 20, 20, "claude-opus-5-5"),
	} {
		u, _ := StepUsageOf(r)
		if _, err := st.RecordStepUsage(ctx, sj.ID, step, u); err != nil {
			t.Fatal(err)
		}
	}
	cc := &captureCloser{}
	f := &Finisher{Closer: cc, Store: st, Log: quiet}
	h := executor.Handle{JobID: sj.ID + "f1"}
	if _, err := f.Finish(ctx, sj.ID, h, executor.Status{State: executor.Exited}, usageResult(2, 20, 20, 20, 20, "claude-opus-5-5"), closeout.Cause{}); err != nil {
		t.Fatal(err)
	}
	j := cc.got[0]
	if j.Usage.Steps != 2 || j.Usage.Tokens.Input != 30 || *j.Usage.CostUSD != 3 || j.CostUSD != 3 {
		t.Errorf("usage = %+v tokens=%+v cost=%v", j.Usage, j.Usage.Tokens, j.CostUSD)
	}
}
