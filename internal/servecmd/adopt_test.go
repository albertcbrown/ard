package servecmd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/pool"
	"github.com/albertcbrown/ard/internal/state"
)

type lister struct {
	jobs []state.Job
	err  error
}

func (l lister) RunningJobs(context.Context) ([]state.Job, error) { return l.jobs, l.err }

func TestHeldJobs(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name    string
		jobs    []state.Job
		want    []pool.Candidate
		wantErr string
	}{
		{"review uses PR", []state.Job{{ID: "a", Stage: "review", Role: "sec", Issue: 5, PR: 9, CreatedAt: at}},
			[]pool.Candidate{{Class: pool.ClassReview, Role: "sec", Number: 9, Since: at}}, ""},
		{"worker and triage use issue", []state.Job{
			{ID: "a", Stage: "worker", Role: "w", Issue: 5, CreatedAt: at},
			{ID: "b", Stage: "triage", Role: "t", Issue: 6, CreatedAt: at}},
			[]pool.Candidate{{Class: pool.ClassWorker, Role: "w", Number: 5, Since: at},
				{Class: pool.ClassTriage, Role: "t", Number: 6, Since: at}}, ""},
		{"scanner uses 0", []state.Job{{ID: "a", Stage: "scanner", Role: "cve", CreatedAt: at}},
			[]pool.Candidate{{Class: pool.ClassScanner, Role: "cve", Since: at}}, ""},
		{"unknown stage", []state.Job{{ID: "job-x", Stage: "merge", Role: "m", Issue: 1}}, nil, "job-x"},
		{"review without PR", []state.Job{{ID: "job-y", Stage: "review", Role: "r", Issue: 1}}, nil, "job-y"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := heldJobs(context.Background(), lister{jobs: tc.jobs})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want mention of %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || len(got) != len(tc.want) {
				t.Fatalf("got %v, %v", got, err)
			}
			for i := range got {
				if got[i].Candidate != tc.want[i] || got[i].JobID != tc.jobs[i].ID {
					t.Errorf("[%d] = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestAdoptPoolHoldsRunningJobs(t *testing.T) {
	at := time.Now()
	l := lister{jobs: []state.Job{
		{ID: "a", Stage: "worker", Role: "w", Issue: 1, CreatedAt: at},
		{ID: "b", Stage: "worker", Role: "w", Issue: 2, CreatedAt: at},
	}}
	p, err := adoptPool(context.Background(), l, 2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if st := p.Stats(); st.Held != 2 || st.Free != 0 {
		t.Errorf("stats = %+v", st)
	}
	if _, err := adoptPool(context.Background(), lister{err: errors.New("db")}, 2, nil); err == nil {
		t.Error("want lister error")
	}
}
