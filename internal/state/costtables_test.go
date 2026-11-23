package state

import (
	"testing"
	"time"
)

func TestCostTableMarking(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	w, _ := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "engineer", Issue: 7})
	sc, _ := s.CreateJob(ctx, NewJob{Stage: "scanner", Role: "cve"})

	if err := s.SetCloseout(ctx, w.ID, "prepared", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if d, err := s.DirtyCostTables(ctx, 10); err != nil || len(d) != 0 {
		t.Fatalf("before closed: %v, %v", d, err)
	}
	if err := s.SetCloseout(ctx, sc.ID, "closed", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if d, _ := s.DirtyCostTables(ctx, 10); len(d) != 0 {
		t.Fatalf("scanner job marked: %v", d)
	}
	if err := s.SetCloseout(ctx, w.ID, "closed", []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	d, err := s.DirtyCostTables(ctx, 10)
	if err != nil || len(d) != 1 || d[0].Issue != 7 {
		t.Fatalf("dirty = %v, %v", d, err)
	}
	got, _ := s.Job(ctx, w.ID)
	if got.CloseoutPhase != "closed" || string(got.CloseoutPlan) != `{"a":1}` {
		t.Fatalf("job = %+v", got)
	}
	if err := s.SetCloseout(ctx, "nope", "closed", nil); err == nil {
		t.Fatal("unknown job accepted")
	}

	// A stale marked_at leaves it dirty; the current one clears it.
	if err := s.CostTableDone(ctx, 7, d[0].MarkedAt.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if d2, _ := s.DirtyCostTables(ctx, 10); len(d2) != 1 {
		t.Fatalf("stale done cleared the flag: %v", d2)
	}
	if n, err := s.CostTableFailed(ctx, 7); err != nil || n != 1 {
		t.Fatalf("failed = %d, %v", n, err)
	}
	if err := s.CostTableDone(ctx, 7, d[0].MarkedAt); err != nil {
		t.Fatal(err)
	}
	if d2, _ := s.DirtyCostTables(ctx, 10); len(d2) != 0 {
		t.Fatalf("done left dirty: %v", d2)
	}
	// A replay re-marks and resets failures.
	if err := s.SetCloseout(ctx, w.ID, "closed", nil); err != nil {
		t.Fatal(err)
	}
	if d2, _ := s.DirtyCostTables(ctx, 10); len(d2) != 1 || d2[0].Failures != 0 {
		t.Fatalf("replay: %v", d2)
	}
}

func TestClosedJobsAndIssueUsage(t *testing.T) {
	ctx := tctx(t)
	s := newStore(t)
	a, _ := s.CreateJob(ctx, NewJob{Stage: "triage", Role: "lead", Issue: 7})
	b, _ := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "engineer", Issue: 7})
	c, _ := s.CreateJob(ctx, NewJob{Stage: "worker", Role: "engineer", Issue: 8})
	_ = s.SetCloseout(ctx, a.ID, "closed", []byte(`{}`))
	_ = s.SetCloseout(ctx, b.ID, "committed", []byte(`{}`))
	_ = s.SetCloseout(ctx, c.ID, "closed", []byte(`{}`))
	js, err := s.ClosedJobsOf(ctx, 7)
	if err != nil || len(js) != 1 || js[0].ID != a.ID {
		t.Fatalf("closed = %v, %v", js, err)
	}
	for _, j := range []Job{a, b, c} {
		if _, err := s.RecordStepUsage(ctx, j.ID, j.ID, StepUsage{Models: []string{"m1"}, Tokens: &Tokens{1, 2, 3, 4}, CostUSD: fp(0.5)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RecordStepUsage(ctx, a.ID, a.ID+"f1", StepUsage{Models: []string{"m0"}, CostUSD: fp(0.25)}); err != nil {
		t.Fatal(err)
	}
	u, err := s.IssueUsage(ctx, 7)
	if err != nil || len(u) != 2 {
		t.Fatalf("usage = %v, %v", u, err)
	}
	ua := u[a.ID]
	one, _ := s.JobUsage(ctx, a.ID)
	if ua.Steps != 2 || *ua.CostUSD != 0.75 || ua.Tokens.Input != 1 || len(ua.Models) != 2 || ua.Models[0] != "m0" || *one.CostUSD != *ua.CostUSD {
		t.Fatalf("ua = %+v one = %+v", ua, one)
	}
}
