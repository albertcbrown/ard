package servecmd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/executor"
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
	p, err := adoptPool(context.Background(), l, 2, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatal(err)
	}
	if st := p.Stats(); st.Held != 2 || st.Free != 0 {
		t.Errorf("stats = %+v", st)
	}
	if _, err := adoptPool(context.Background(), lister{err: errors.New("db")}, 2, nil, nil); err == nil {
		t.Error("want lister error")
	}
}

type failRecorder struct {
	lister
	failed   []string
	reasons  []string
	cleaned  []string
	cleanErr error
}

func (f *failRecorder) Fail(_ context.Context, id, reason string, _ time.Time) error {
	f.failed, f.reasons = append(f.failed, id), append(f.reasons, reason)
	return nil
}

func (f *failRecorder) Cleanup(_ context.Context, h executor.Handle, _ executor.CleanupOptions) error {
	f.cleaned = append(f.cleaned, h.JobID)
	return f.cleanErr
}

func TestStaleScannerJobsAreFailedNotHeld(t *testing.T) {
	at := time.Now()
	h, _ := json.Marshal(executor.Handle{JobID: "s1", Worktree: "/w/s1"})
	r := &failRecorder{lister: lister{jobs: []state.Job{
		{ID: "s1", Stage: "scanner", Role: "cve", CreatedAt: at, Handle: h},
		{ID: "s2", Stage: "scanner", Role: "lint", CreatedAt: at}, // no handle: nothing to clean
		{ID: "w1", Stage: "worker", Role: "w", Issue: 1, CreatedAt: at},
	}}, cleanErr: errors.New("already gone")}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := failStaleScannerJobs(context.Background(), r, r, log); err != nil {
		t.Fatal(err)
	}
	if len(r.failed) != 2 || r.failed[0] != "s1" || r.failed[1] != "s2" || r.reasons[0] != "controller restarted" {
		t.Errorf("failed %v %v", r.failed, r.reasons)
	}
	if len(r.cleaned) != 1 || r.cleaned[0] != "s1" {
		t.Errorf("cleaned %v (a cleanup error must not stop the retirement)", r.cleaned)
	}
	held, err := heldJobs(context.Background(), r)
	if err != nil || len(held) != 1 || held[0].Candidate.Class != pool.ClassWorker {
		t.Errorf("held = %+v, %v: a scanner job must never be held", held, err)
	}
	// With the stale job out of the way the scanner can be offered a slot.
	p, _ := adoptPool(context.Background(), r, 2, log, nil)
	d := p.Offer("scanners", time.Minute, []pool.Candidate{{Class: pool.ClassScanner, Role: "cve", Since: at}})
	if d[0].Verdict != pool.Granted {
		t.Errorf("verdict = %v", d[0].Verdict)
	}
}

func TestAdoptPoolPassesLimited(t *testing.T) {
	until := time.Now().Add(time.Hour)
	p, err := adoptPool(context.Background(), lister{}, 2, slog.New(slog.NewTextHandler(io.Discard, nil)), func(pool.Candidate) time.Time { return until })
	if err != nil {
		t.Fatal(err)
	}
	c := pool.Candidate{Class: pool.ClassWorker, Role: "eng", Number: 1, Since: time.Now()}
	if ds := p.Offer("w", time.Minute, []pool.Candidate{c}); ds[0].Verdict != pool.DeniedLimited {
		t.Errorf("verdict = %v, want limited", ds[0].Verdict)
	}
}
