package closeout

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/state"
)

// seedRecord stores a close-out record for a job of the env's issue, created
// now, so later claims sort after it by ID.
func (e *env) seedRecord(stage, role string, phase Phase, p Plan) string {
	e.t.Helper()
	ctx := context.Background()
	sj, err := e.store.CreateJob(ctx, state.NewJob{Stage: stage, Role: role, Issue: e.issue})
	if err != nil {
		e.t.Fatal(err)
	}
	b, err := json.Marshal(Record{Version: recordVersion, Phase: phase, Job: Job{ID: sj.ID, Stage: stage, Role: role, Issue: e.issue}, Plan: p})
	if err != nil {
		e.t.Fatal(err)
	}
	if err := e.store.SetCloseout(ctx, sj.ID, string(phase), b); err != nil {
		e.t.Fatal(err)
	}
	if phase == PhaseClosed {
		if err := e.store.Complete(ctx, sj.ID, nil); err != nil {
			e.t.Fatal(err)
		}
	}
	return sj.ID
}

func (e *env) storedRecord(id string) Record {
	e.t.Helper()
	rec, err := decode(e.sjob(id))
	if err != nil {
		e.t.Fatal(err)
	}
	return rec
}

// openRound seeds a lead and architect done, then a bounced worker record in
// the given phase.
func (e *env) openRound(phase Phase) {
	e.seedRecord(StageTriage, "lead", PhaseClosed, Plan{Outcome: OutcomeDone})
	e.seedRecord(StageTriage, "architect", PhaseClosed, Plan{Outcome: OutcomeDone})
	e.seedRecord(StageWorker, "engineer", phase, Plan{Outcome: OutcomeNeedsNetwork, Bounced: true, Network: needSpec})
}

func TestPrepareRoundOpen(t *testing.T) {
	// A worker record that never closed (phase prepared) still opens the round.
	for _, phase := range []Phase{PhaseClosed, PhasePrepared} {
		e := newEnv(t)
		e.d.Network = e.store
		e.openRound(phase)
		e.write(".ard/7/triage-engineer.md", roleFile("engineer", "done",
			"inputs:\n  - path: data/spec.txt\n    provider: human"))
		j := e.claim(StageTriage, "engineer")
		if out, err := New(e.d).Run(context.Background(), j); err != nil || out != OutcomeDone {
			t.Fatalf("phase %s: Run = %s, %v", phase, out, err)
		}
		rec := e.storedRecord(j.ID)
		if rec.Job.Network == nil || !rec.Job.Network.Round || !reflect.DeepEqual(rec.Job.Network.Done, []string{"architect"}) {
			t.Fatalf("phase %s: persisted network state = %+v", phase, rec.Job.Network)
		}
		if !rec.Plan.Rearmed || !reflect.DeepEqual(rec.Plan.Rerun, []string{"architect"}) ||
			!reflect.DeepEqual(rec.Plan.Inputs, []Input{{Path: "data/spec.txt", Provider: "human"}}) {
			t.Errorf("phase %s: plan = %+v", phase, rec.Plan)
		}
		l := e.labels(7)
		if !l["ard:pending"] || l["ard:triage:lead:done"] || !l["ard:triage:engineer:done"] {
			t.Errorf("phase %s: labels = %v", phase, l)
		}
	}
}

func TestPrepareNoRoundNoRearm(t *testing.T) {
	e := newEnv(t)
	e.d.Network = e.store
	e.seedRecord(StageTriage, "lead", PhaseClosed, Plan{Outcome: OutcomeDone})
	e.write(".ard/7/triage-engineer.md", roleFile("engineer", "done"))
	j := e.claim(StageTriage, "engineer")
	if _, err := New(e.d).Run(context.Background(), j); err != nil {
		t.Fatal(err)
	}
	if rec := e.storedRecord(j.ID); rec.Plan.Rearmed || (rec.Job.Network != nil && rec.Job.Network.Round) {
		t.Errorf("rearmed without a round: %+v %+v", rec.Plan, rec.Job.Network)
	}
}

func TestRearmConsumedByFirstEngineerDone(t *testing.T) {
	e := newEnv(t)
	e.d.Network = e.store
	e.openRound(PhaseClosed)
	e.write(".ard/7/triage-engineer.md", roleFile("engineer", "done"))
	first := e.claim(StageTriage, "engineer")
	if _, err := New(e.d).Run(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	// A human re-requests the engineer: no open round, no second re-arm.
	second := e.claim(StageTriage, "engineer")
	if _, err := New(e.d).Run(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if rec := e.storedRecord(second.ID); rec.Plan.Rearmed || rec.Job.Network != nil && rec.Job.Network.Round {
		t.Errorf("second engineer done re-armed: %+v", rec.Plan)
	}
}

func TestResumeReplaysRound(t *testing.T) {
	e := newEnv(t)
	e.d.Network = e.store
	e.seedRecord(StageTriage, "architect", PhaseClosed, Plan{Outcome: OutcomeDone})
	e.seedRecord(StageTriage, "engineer", PhaseClosed, Plan{Outcome: OutcomeDone, Rearmed: true, Rerun: []string{"architect"}})
	e.write(".ard/7/triage-lead.md", roleFile("lead", "done", "worker: engineer"))
	j := e.claim(StageTriage, "lead")
	e.d = e.deps(0)
	e.d.Network = e.store
	e.fstore.failAt = 3 // prepared, committed, then fail while saving "requested"
	c := New(e.d)
	if _, err := c.Run(context.Background(), j); err == nil {
		t.Fatal("expected the injected store failure")
	}
	e.fstore.failAt = 0
	rec := e.storedRecord(j.ID)
	if rec.Job.Network == nil || !reflect.DeepEqual(rec.Job.Network.Rerun, []string{"architect"}) {
		t.Fatalf("persisted network state = %+v", rec.Job.Network)
	}
	// The store changes: a newer re-arm names another role, so a fresh
	// LeadRerun would say designer, not architect.
	e.seedRecord(StageTriage, "engineer", PhaseClosed, Plan{Outcome: OutcomeDone, Rearmed: true, Rerun: []string{"designer"}})
	if got, _ := LeadRerun(context.Background(), e.store, 7); !reflect.DeepEqual(got, []string{"designer"}) {
		t.Fatalf("LeadRerun after the change = %v", got)
	}
	if out, err := c.ResumeJob(context.Background(), j.ID); err != nil || out != OutcomeDone {
		t.Fatalf("ResumeJob = %s, %v", out, err)
	}
	l := e.labels(7)
	if !l["ard:triage:architect"] || l["ard:triage:architect:done"] || l["ard:triage:designer"] || !l["ard:triage:lead:done"] {
		t.Errorf("replay did not use the stored plan: labels %v", l)
	}
}

func TestInputsRedacted(t *testing.T) {
	e := newEnv(t)
	e.d.Network = e.store
	e.write(".ard/7/triage-engineer.md", roleFile("engineer", "done",
		"inputs:\n  - path: a.txt\n    provider: worker\n    url: https://example.com/x?key="+secretValue))
	j := e.claim(StageTriage, "engineer")
	if out, err := New(e.d).Run(context.Background(), j); err != nil || out != OutcomeDone {
		t.Fatalf("Run = %s, %v", out, err)
	}
	rec := e.storedRecord(j.ID)
	b, _ := json.Marshal(rec.Plan)
	if strings.Contains(string(b), secretValue) || len(rec.Plan.Inputs) != 1 || !strings.Contains(rec.Plan.Inputs[0].URL, "[REDACTED") {
		t.Errorf("stored inputs not redacted: %s", b)
	}
}

// An input list that no longer parses after redaction ends needs-human.
func TestInputsLostNeedsHuman(t *testing.T) {
	e := newEnv(t)
	e.d.Network = e.store
	e.write(".ard/7/triage-engineer.md", roleFile("engineer", "done",
		"inputs:\n  - path: "+secretValue+"\n    provider: human"))
	j := e.claim(StageTriage, "engineer")
	if out, err := New(e.d).Run(context.Background(), j); err != nil || out != OutcomeNeedsHuman {
		t.Fatalf("Run = %s, %v", out, err)
	}
	rec := e.storedRecord(j.ID)
	if !strings.Contains(rec.Plan.Reason, "inputs list unreadable") || len(rec.Plan.Inputs) != 0 {
		t.Errorf("plan = %+v", rec.Plan)
	}
}

func TestInputsOfAndNetworkRoundCount(t *testing.T) {
	ctx := context.Background()
	s := stageStore{
		StageWorker: {netRecord(t, "03", PhaseCommented, OutcomeNeedsNetwork, true)},
		StageTriage: {
			triageRec(t, "01", "engineer", OutcomeDone, Plan{Inputs: []Input{{Path: "old.txt", Provider: "human"}}}),
			triageRec(t, "04", "engineer", OutcomeDone, Plan{Inputs: []Input{{Path: "new.txt", Provider: "human"}}}),
			triageRec(t, "05", "lead", OutcomeDone, Plan{}),
		},
	}
	got, ok, err := InputsOf(ctx, s, 7)
	if err != nil || !ok || len(got) != 1 || got[0].Path != "new.txt" {
		t.Errorf("InputsOf = %v, %v, %v", got, ok, err)
	}
	if _, ok, err := InputsOf(ctx, stageStore{}, 7); err != nil || ok {
		t.Errorf("InputsOf none = %v, %v", ok, err)
	}
	if n, err := NetworkRoundCount(ctx, s, 7); err != nil || n != 1 {
		t.Errorf("NetworkRoundCount = %d, %v", n, err)
	}
}
