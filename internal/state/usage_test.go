package state

import (
	"testing"
	"time"
)

func fp(v float64) *float64 { return &v }

func TestRecordStepUsage(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	j, err := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "engineer", Issue: 7})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := s.JobUsage(ctx, j.ID)
	if err != nil || empty.Steps != 0 || empty.Tokens != nil || empty.CostUSD != nil {
		t.Fatalf("empty usage = %+v, %v", empty, err)
	}
	steps := []struct {
		step string
		u    StepUsage
	}{
		{j.ID, StepUsage{Models: []string{"claude-opus-5-5", "bad model!"}, Tokens: &Tokens{1, 2, 3, 4}, CostUSD: fp(0.5)}},
		{j.ID + "f1", StepUsage{Models: []string{"claude-haiku-4-5", "claude-opus-5-5"}, Tokens: &Tokens{10, 20, 30, 40}, CostUSD: fp(0.25)}},
		{j.ID + "f2", StepUsage{}},
	}
	for _, st := range steps {
		if rec, err := s.RecordStepUsage(ctx, j.ID, st.step, st.u); err != nil || !rec {
			t.Fatalf("record %s = %v, %v", st.step, rec, err)
		}
	}
	// Idempotent: a repeat does not change the sums.
	if rec, err := s.RecordStepUsage(ctx, j.ID, j.ID, StepUsage{Tokens: &Tokens{99, 99, 99, 99}, CostUSD: fp(9)}); err != nil || rec {
		t.Fatalf("repeat = %v, %v", rec, err)
	}
	u, err := s.JobUsage(ctx, j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if u.Steps != 3 || *u.Tokens != (Tokens{11, 22, 33, 44}) || *u.CostUSD != 0.75 {
		t.Errorf("usage = %+v tokens=%+v", u, u.Tokens)
	}
	if len(u.Models) != 2 || u.Models[0] != "claude-haiku-4-5" || u.Models[1] != "claude-opus-5-5" {
		t.Errorf("models = %v", u.Models)
	}
	got, _ := s.Job(ctx, j.ID)
	if got.CostUSD == nil || *got.CostUSD != 0.75 {
		t.Errorf("jobs.cost_usd = %v", got.CostUSD)
	}
	for _, bad := range []struct {
		step string
		u    StepUsage
	}{{"", StepUsage{}}, {"a b", StepUsage{}}, {"x", StepUsage{Tokens: &Tokens{Input: -1}}}, {"x", StepUsage{CostUSD: fp(-1)}}} {
		if _, err := s.RecordStepUsage(ctx, j.ID, bad.step, bad.u); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

func TestUsageKeptByEveryClose(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(s *Store, id string) error
	}{
		{"fail", func(s *Store, id string) error { return s.Fail(tctx(t), id, "boom", time.Time{}) }},
		{"retry", func(s *Store, id string) error { return s.FailRetry(tctx(t), id, "boom", time.Time{}) }},
		{"limited", func(s *Store, id string) error { return s.FailLimited(tctx(t), id, "boom", time.Time{}) }},
		{"complete-nil", func(s *Store, id string) error { return s.Complete(tctx(t), id, nil) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := tctx(t)
			s := newStore(t)
			j, err := s.CreateJob(ctx, NewJob{Stage: "triage", Role: "lead", Issue: 3})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordStepUsage(ctx, j.ID, j.ID, StepUsage{CostUSD: fp(1.5)}); err != nil {
				t.Fatal(err)
			}
			if err := tc.close(s, j.ID); err != nil {
				t.Fatal(err)
			}
			got, _ := s.Job(ctx, j.ID)
			if got.CostUSD == nil || *got.CostUSD != 1.5 {
				t.Errorf("cost = %v", got.CostUSD)
			}
		})
	}
}

func TestUsageSurvivesReopen(t *testing.T) {
	ctx := tctx(t)
	dir := t.TempDir()
	s, _ := newStoreClock(t, dir)
	j, err := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "engineer", Issue: 7})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordStepUsage(ctx, j.ID, j.ID, StepUsage{Models: []string{"m1"}, Tokens: &Tokens{1, 2, 3, 4}, CostUSD: fp(0.1)}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, _ := newStoreClock(t, dir)
	u, err := s2.JobUsage(ctx, j.ID)
	if err != nil || u.Steps != 1 || *u.Tokens != (Tokens{1, 2, 3, 4}) || *u.CostUSD != 0.1 || u.Models[0] != "m1" {
		t.Errorf("after reopen = %+v, %v", u, err)
	}
}
