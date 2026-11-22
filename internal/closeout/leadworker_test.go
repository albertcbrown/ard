package closeout

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/albertcbrown/ard/internal/state"
)

type fakeCloseouts struct{ jobs []state.Job }

func (f fakeCloseouts) CloseoutsOf(context.Context, int, string) ([]state.Job, error) {
	return f.jobs, nil
}

func closeoutJob(t *testing.T, id, role string, phase Phase, out Outcome, worker string) state.Job {
	t.Helper()
	b, err := json.Marshal(Record{Version: recordVersion, Phase: phase, Job: Job{ID: id, Role: role, Stage: StageTriage},
		Plan: Plan{Outcome: out, Worker: worker}})
	if err != nil {
		t.Fatal(err)
	}
	return state.Job{ID: id, CloseoutPlan: b}
}

func TestLeadWorker(t *testing.T) {
	tests := []struct {
		name       string
		jobs       []state.Job
		wantWorker string
		wantID     string
		wantOK     bool
		wantErr    bool
	}{
		{name: "none"},
		{name: "one done", jobs: []state.Job{closeoutJob(t, "A", "lead", PhaseClosed, OutcomeDone, "engineer")}, wantWorker: "engineer", wantID: "A", wantOK: true},
		{name: "done with no worker", jobs: []state.Job{closeoutJob(t, "A", "lead", PhaseClosed, OutcomeDone, "")}, wantID: "A", wantOK: true},
		{name: "needs-human lead ignored", jobs: []state.Job{closeoutJob(t, "A", "lead", PhaseClosed, OutcomeNeedsHuman, "engineer")}},
		{name: "newest done lead wins", jobs: []state.Job{
			closeoutJob(t, "A", "lead", PhaseClosed, OutcomeDone, "engineer"),
			closeoutJob(t, "B", "lead", PhaseClosed, OutcomeDone, "docs")}, wantWorker: "docs", wantID: "B", wantOK: true},
		{name: "not yet closed ignored", jobs: []state.Job{
			closeoutJob(t, "A", "lead", PhaseClosed, OutcomeDone, "engineer"),
			closeoutJob(t, "B", "lead", PhaseCommitted, OutcomeDone, "docs")}, wantWorker: "engineer", wantID: "A", wantOK: true},
		{name: "other role ignored", jobs: []state.Job{closeoutJob(t, "A", "engineer", PhaseClosed, OutcomeDone, "docs")}},
		{name: "undecodable", jobs: []state.Job{{ID: "X", CloseoutPlan: []byte("{")}}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, id, ok, err := LeadWorker(context.Background(), fakeCloseouts{tc.jobs}, 7)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v", err)
			}
			if w != tc.wantWorker || id != tc.wantID || ok != tc.wantOK {
				t.Errorf("got %q %q %v, want %q %q %v", w, id, ok, tc.wantWorker, tc.wantID, tc.wantOK)
			}
		})
	}
}
