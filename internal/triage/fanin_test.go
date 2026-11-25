package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/costblock"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

func TestDecideFanIn(t *testing.T) {
	triage := map[string]bool{"lead": true, "architect": true, "engineer": true}
	workers := map[string]bool{"engineer": true}
	done := labels.Done(labels.StageTriage, "lead")
	tests := []struct {
		name   string
		labels []string
		busy   bool
		worker string
		want   fanInVerdict
		reason string
		role   string
	}{
		{"lead not done", []string{"ard:triage:architect:done"}, false, "engineer", fSkip, rLeadNotDone, ""},
		{"needs-human", []string{done, labels.NeedsHuman}, false, "engineer", fSkip, rGuarded, ""},
		{"hold", []string{done, labels.Hold}, false, "engineer", fSkip, rGuarded, ""},
		{"executor failed", []string{done, labels.ExecutorFailed}, false, "engineer", fSkip, rGuarded, ""},
		{"worker requested", []string{done, "ard:worker:engineer"}, false, "engineer", fSkip, rWorkerStarted, ""},
		{"worker running", []string{done, "ard:worker:engineer:running"}, false, "engineer", fSkip, rWorkerStarted, ""},
		{"fix round, worker done only", []string{done, "ard:worker:engineer:done"}, false, "engineer", fSkip, rWorkerStarted, ""},
		{"lead re-requested", []string{done, labels.Pending}, false, "engineer", fSkip, rLeadRequested, ""},
		{"busy", []string{done}, true, "engineer", fWait, rTriageRunning, ""},
		{"triage label running", []string{done, "ard:triage:architect", "ard:triage:architect:running"}, false, "engineer", fWait, rTriageRunning, ""},
		{"requested role not done", []string{done, "ard:triage:architect"}, false, "engineer", fWait, rWaiting, "architect"},
		{"mixed case request", []string{done, "ARD:Triage:Architect"}, false, "engineer", fWait, rWaiting, "architect"},
		{"requested role done", []string{done, "ard:triage:architect", "ard:triage:architect:done"}, false, "engineer", fReady, rReady, ""},
		{"unknown role", []string{done, "ard:triage:ghost"}, false, "engineer", fHuman, rUnknownRole, "ghost"},
		{"unknown wins over waiting", []string{done, "ard:triage:architect", "ard:triage:ghost"}, false, "engineer", fHuman, rUnknownRole, "ghost"},
		{"no worker", []string{done}, false, "", fHuman, rNoWorker, ""},
		{"worker gone", []string{done}, false, "docs", fHuman, rWorkerGone, ""},
		{"lead only, valid worker", []string{done}, false, "engineer", fReady, rReady, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decideFanIn(fanInInput{Labels: tc.labels, Triage: triage, Workers: workers, Busy: tc.busy, Worker: tc.worker})
			if got.Verdict != tc.want || got.Reason != tc.reason || got.Role != tc.role {
				t.Errorf("got %+v, want %v %s %s", got, tc.want, tc.reason, tc.role)
			}
			if got.Verdict == fReady && got.Worker != tc.worker {
				t.Errorf("worker = %q", got.Worker)
			}
		})
	}
}

func TestApprovalEvent(t *testing.T) {
	ev := func(kind, label, actor string, min int) github.TimelineEvent {
		return github.TimelineEvent{Event: kind, Label: label, Actor: actor, CreatedAt: t0.Add(time.Duration(min) * time.Minute)}
	}
	tests := []struct {
		name  string
		tl    []github.TimelineEvent
		actor string
		ok    bool
	}{
		{"none", nil, "", false},
		{"one labeled", []github.TimelineEvent{ev("labeled", labels.Approved, "a", 1)}, "a", true},
		{"labeled then unlabeled", []github.TimelineEvent{ev("labeled", labels.Approved, "a", 1), ev("unlabeled", labels.Approved, "a", 2)}, "", false},
		{"relabeled takes the latest", []github.TimelineEvent{ev("labeled", labels.Approved, "a", 1), ev("unlabeled", labels.Approved, "a", 2), ev("labeled", labels.Approved, "b", 3)}, "b", true},
		{"other labels ignored", []github.TimelineEvent{ev("labeled", labels.Approved, "a", 1), ev("labeled", labels.Hold, "b", 2), ev("unlabeled", labels.Hold, "b", 3)}, "a", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := approvalEvent(tc.tl)
			if ok != tc.ok || got.Actor != tc.actor {
				t.Errorf("got %q %v", got.Actor, ok)
			}
		})
	}
}

func TestLastTriageDone(t *testing.T) {
	tl := []github.TimelineEvent{
		{Event: "labeled", Label: "ard:triage:lead:done", CreatedAt: t0.Add(time.Minute)},
		{Event: "labeled", Label: "ard:triage:engineer:done", CreatedAt: t0.Add(3 * time.Minute)},
		{Event: "labeled", Label: "ard:worker:engineer:done", CreatedAt: t0.Add(9 * time.Minute)},
		{Event: "unlabeled", Label: "ard:triage:architect:done", CreatedAt: t0.Add(7 * time.Minute)},
	}
	if at, ok := lastTriageDone(tl); !ok || !at.Equal(t0.Add(3*time.Minute)) {
		t.Errorf("got %v %v", at, ok)
	}
	if _, ok := lastTriageDone(nil); ok {
		t.Error("empty timeline has no done")
	}
}

// gateHarness is a harness with a worker role engineer, an engineer triage role
// and require_approval as given.
func gateHarness(t *testing.T, approval bool, opts ...option) *harness {
	t.Helper()
	h := newHarness(t, opts...)
	h.cfg.RequireApproval = approval
	h.cfg.Roles = append(h.cfg.Roles,
		config.Role{Kind: config.KindWorker, Name: "engineer", Dir: ".ard/workers/engineer", Agent: "claude", Timeout: time.Minute},
		config.Role{Kind: config.KindTriage, Name: "engineer", Dir: ".ard/triage/engineer", Agent: "claude", Timeout: time.Minute})
	return h
}

var (
	leadDone = labels.Done(labels.StageTriage, "lead")
	tDone    = t0.Add(time.Hour) // when the last triage :done was added
)

// seedLead records the lead's closed close-out with the given worker.
func (h *harness) seedLead(n int, worker string) string {
	h.t.Helper()
	ctx := context.Background()
	j, err := h.store.CreateJob(ctx, state.NewJob{Stage: "triage", Role: "lead", Issue: n})
	if err != nil {
		h.t.Fatal(err)
	}
	rec, _ := json.Marshal(closeout.Record{Version: 1, Phase: closeout.PhaseClosed,
		Job:  closeout.Job{ID: j.ID, Issue: n, Stage: "triage", Role: "lead"},
		Plan: closeout.Plan{Outcome: closeout.OutcomeDone, Worker: worker}})
	if err := h.store.SetCloseout(ctx, j.ID, "closed", rec); err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.Complete(ctx, j.ID, nil); err != nil {
		h.t.Fatal(err)
	}
	return j.ID
}

// ready seeds an App-authored issue whose triage is finished, with the lead's
// close-out naming engineer.
func (h *harness) ready(extra ...string) int {
	h.t.Helper()
	n := h.gh.SeedIssue(github.Issue{Title: "t", Body: "b", Author: self, Labels: append([]string{leadDone}, extra...), CreatedAt: t0, UpdatedAt: t0})
	h.gh.SeedTimeline(n, github.TimelineEvent{Event: "labeled", Label: leadDone, Actor: self, CreatedAt: tDone})
	h.seedLead(n, "engineer")
	return n
}

func (h *harness) events(kind string) []state.Event {
	h.t.Helper()
	es, err := h.store.Events(context.Background(), state.EventFilter{Kind: kind})
	if err != nil {
		h.t.Fatal(err)
	}
	return es
}

func (h *harness) writes() int { return len(h.gh.Writes()) }

func commentsWith(h *harness, n int, sub string) int {
	c := 0
	for _, cm := range h.comments(n) {
		if strings.Contains(cm.Body, sub) {
			c++
		}
	}
	return c
}

func TestGateStartsWorker(t *testing.T) {
	h := gateHarness(t, false)
	n := h.ready("ard:triage:engineer", "ard:triage:engineer:done")
	h.mustTick()
	if !h.has(n, "ard:worker:engineer") {
		t.Fatalf("worker not requested: %v", h.issue(n).Labels)
	}
	if es := h.events(state.EventGate); len(es) != 1 || !strings.Contains(string(es[0].Data), `"verdict":"start"`) {
		t.Errorf("events = %+v", es)
	}
	w := h.writes()
	h.mustTick()
	if h.writes() != w {
		t.Errorf("second tick wrote: %v", h.gh.Writes()[w:])
	}
}

func TestGateNoPlan(t *testing.T) {
	for _, approval := range []bool{true, false} {
		h := gateHarness(t, approval)
		n := h.ready()
		h.noPlan = map[int]bool{n: true}
		h.mustTick()
		h.mustTick()
		if !h.has(n, labels.NeedsHuman) || h.has(n, "ard:worker:engineer") || commentsWith(h, n, "ready for approval") != 0 {
			t.Errorf("approval=%v labels %v", approval, h.issue(n).Labels)
		}
		if commentsWith(h, n, "plan.md` does not exist") != 1 {
			t.Errorf("comments = %+v", h.comments(n))
		}
		if es := h.events(state.EventGate); len(es) != 1 || !strings.Contains(string(es[0].Data), `"reason":"no-plan"`) {
			t.Errorf("events = %+v", es)
		}
	}
	t.Run("check error writes nothing", func(t *testing.T) {
		h := gateHarness(t, true)
		h.ready()
		h.planErr = errors.New("boom")
		if err := h.tick(); err == nil {
			t.Error("want a tick error")
		}
		if h.writes() != 0 {
			t.Errorf("wrote %v", h.gh.Writes())
		}
	})
	t.Run("waiting role skips the check", func(t *testing.T) {
		h := gateHarness(t, true)
		n := h.ready("ard:triage:engineer")
		h.noPlan = map[int]bool{n: true}
		h.planErr = errors.New("must not be called")
		h.mustTick()
		if h.has(n, labels.NeedsHuman) || len(h.comments(n)) != 0 {
			t.Errorf("gate acted: %v", h.gh.Writes())
		}
	})
}

func TestGateWaits(t *testing.T) {
	tests := []struct {
		name  string
		extra []string
		setup func(*harness, int)
	}{
		{"engineer requested, not done", []string{"ard:triage:engineer"}, nil},
		{"triage running label", []string{"ard:triage:engineer:running"}, nil},
		{"triage job running in state", nil, func(h *harness, n int) {
			if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "triage", Role: "engineer", Issue: n}); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := gateHarness(t, false)
			n := h.ready(tc.extra...)
			if tc.setup != nil {
				tc.setup(h, n)
			}
			h.mustTick()
			// A triage claim of a requested role may write; the gate must not.
			if h.has(n, "ard:worker:engineer") || h.has(n, labels.NeedsHuman) || len(h.comments(n)) != 0 {
				t.Errorf("gate acted: %v", h.gh.Writes())
			}
		})
	}
}

func TestGateApproval(t *testing.T) {
	// The approved label is seeded with the issue, with a timeline event at a
	// chosen time.
	seeded := func(h *harness, actor, role string, at time.Time) int {
		n := h.ready(labels.Approved)
		h.labelledBy(n, labels.Approved, actor, role, at)
		return n
	}
	after := tDone.Add(time.Minute)

	t.Run("no approval posts one comment and waits", func(t *testing.T) {
		h := gateHarness(t, true)
		n := h.ready()
		h.mustTick()
		h.mustTick()
		if h.has(n, "ard:worker:engineer") || commentsWith(h, n, "ready for approval") != 1 {
			t.Errorf("labels %v, comments %d", h.issue(n).Labels, commentsWith(h, n, "ready for approval"))
		}
		if len(h.events(state.EventGate)) != 0 {
			t.Error("waiting recorded an event")
		}
	})
	t.Run("approved by a triage collaborator after triage", func(t *testing.T) {
		h := gateHarness(t, true)
		n := seeded(h, "maint", "triage", after)
		h.mustTick()
		if !h.has(n, "ard:worker:engineer") {
			t.Errorf("labels %v", h.issue(n).Labels)
		}
	})
	t.Run("approval before the last triage done is stale: wait, no escalation", func(t *testing.T) {
		h := gateHarness(t, true)
		n := seeded(h, "maint", "triage", tDone.Add(-time.Minute))
		h.mustTick()
		h.mustTick()
		if h.has(n, "ard:worker:engineer") || h.has(n, labels.NeedsHuman) || !h.has(n, labels.Approved) {
			t.Errorf("labels %v", h.issue(n).Labels)
		}
		if c := commentsWith(h, n, "ready for approval"); c != 1 {
			t.Errorf("ready comments = %d", c)
		}
	})
	t.Run("a newer done label missing from the timeline waits", func(t *testing.T) {
		h := gateHarness(t, true)
		// Two :done labels on the issue, only the older one in the timeline.
		n := h.ready(labels.Approved, "ard:triage:engineer:done")
		h.labelledBy(n, labels.Approved, "maint", "triage", after)
		h.mustTick()
		if h.has(n, "ard:worker:engineer") {
			t.Error("started")
		}
	})
	t.Run("approval at the same instant as triage done is stale", func(t *testing.T) {
		h := gateHarness(t, true)
		n := seeded(h, "maint", "triage", tDone)
		h.mustTick()
		if h.has(n, "ard:worker:engineer") {
			t.Error("started")
		}
	})
	t.Run("a read-only approver is refused, login not named", func(t *testing.T) {
		h := gateHarness(t, true)
		n := seeded(h, "rando", "read", after)
		h.mustTick()
		h.mustTick()
		if !h.has(n, labels.NeedsHuman) || h.has(n, "ard:worker:engineer") {
			t.Errorf("labels %v", h.issue(n).Labels)
		}
		if commentsWith(h, n, "was not added by a maintainer") != 1 || commentsWith(h, n, "rando") != 0 {
			t.Errorf("comments = %+v", h.comments(n))
		}
	})
	t.Run("approved by the App is refused", func(t *testing.T) {
		h := gateHarness(t, true)
		n := h.ready(labels.Approved)
		h.gh.SeedTimeline(n, github.TimelineEvent{Event: "labeled", Label: labels.Approved, Actor: self, CreatedAt: after})
		h.mustTick()
		if !h.has(n, labels.NeedsHuman) || h.has(n, "ard:worker:engineer") {
			t.Errorf("labels %v", h.issue(n).Labels)
		}
	})
	t.Run("zero permission waits without writing", func(t *testing.T) {
		h := gateHarness(t, true)
		n := h.ready(labels.Approved)
		h.gh.SeedTimeline(n, github.TimelineEvent{Event: "labeled", Label: labels.Approved, Actor: "ghost", CreatedAt: after})
		h.gh.SetPermission("ghost", github.Permission{})
		h.mustTick()
		if h.writes() != 0 {
			t.Errorf("wrote %v", h.gh.Writes())
		}
	})
	t.Run("permission lookup error is a tick error and a wait", func(t *testing.T) {
		h := gateHarness(t, true)
		n := seeded(h, "maint", "triage", after)
		h.gh.FailNext("GetPermission", errors.New("boom"))
		if err := h.tick(); err == nil {
			t.Error("want a tick error")
		}
		if h.has(n, "ard:worker:engineer") || h.writes() != 0 {
			t.Errorf("wrote %v", h.gh.Writes())
		}
	})
	t.Run("no timeline event yet waits", func(t *testing.T) {
		h := gateHarness(t, true)
		h.ready(labels.Approved)
		h.mustTick()
		if h.writes() != 0 {
			t.Errorf("wrote %v", h.gh.Writes())
		}
	})
}

func TestGateHuman(t *testing.T) {
	t.Run("no close-out record", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{leadDone}, CreatedAt: t0})
		h.mustTick()
		h.mustTick()
		if !h.has(n, labels.NeedsHuman) || commentsWith(h, n, "gate=no-worker job=none") != 1 {
			t.Errorf("labels %v comments %+v", h.issue(n).Labels, h.comments(n))
		}
	})
	t.Run("lead named no worker", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{leadDone}, CreatedAt: t0})
		id := h.seedLead(n, "")
		h.mustTick()
		if !h.has(n, labels.NeedsHuman) || commentsWith(h, n, "gate=no-worker job="+id) != 1 {
			t.Errorf("comments %+v", h.comments(n))
		}
	})
	t.Run("worker role gone", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.ready()
		h.cfg.Roles = h.cfg.Roles[:len(h.cfg.Roles)-2] // drop both engineer roles
		h.mustTick()
		if !h.has(n, labels.NeedsHuman) || commentsWith(h, n, "gate=worker-gone worker=engineer") != 1 {
			t.Errorf("comments %+v", h.comments(n))
		}
	})
	t.Run("unknown requested triage role", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.ready("ard:triage:ghost")
		h.mustTick()
		if !h.has(n, labels.NeedsHuman) || commentsWith(h, n, "gate=unknown-role role=ghost") != 1 {
			t.Errorf("comments %+v", h.comments(n))
		}
		if es := h.events(state.EventGate); len(es) != 1 {
			t.Errorf("events %+v", es)
		}
	})
}

func TestGateBodyHash(t *testing.T) {
	t.Run("edited after admission re-gates", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.human("t", "v1", leadDone)
		h.seedLead(n, "engineer")
		if err := h.store.SetAdmission(context.Background(), state.Admission{Issue: n, Mode: "label", Reason: "authorised-labeler", BodyHash: agentjob.BodyHash(github.Issue{Title: "t", Body: "v0"}), LabeledAt: t0}); err != nil {
			t.Fatal(err)
		}
		h.mustTick()
		if h.has(n, "ard:worker:engineer") || !h.has(n, labels.NeedsHuman) || commentsWith(h, n, "changed after triage finished") != 1 {
			t.Errorf("labels %v comments %+v", h.issue(n).Labels, h.comments(n))
		}
		a, err := h.store.Admission(context.Background(), n)
		if err != nil || a.RegatedAt.IsZero() {
			t.Errorf("admission %+v %v", a, err)
		}
	})
	t.Run("unchanged body passes", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.human("t", "v1", leadDone)
		h.seedLead(n, "engineer")
		if err := h.store.SetAdmission(context.Background(), state.Admission{Issue: n, Mode: "label", Reason: "authorised-labeler", BodyHash: agentjob.BodyHash(h.issue(n)), LabeledAt: t0}); err != nil {
			t.Fatal(err)
		}
		h.mustTick()
		if !h.has(n, "ard:worker:engineer") {
			t.Errorf("labels %v", h.issue(n).Labels)
		}
	})
	t.Run("ard's own cost block write does not re-gate", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.human("t", "v1", leadDone)
		h.seedLead(n, "engineer")
		if err := h.store.SetAdmission(context.Background(), state.Admission{Issue: n, Mode: "label", Reason: "authorised-labeler", BodyHash: agentjob.BodyHash(h.issue(n)), LabeledAt: t0}); err != nil {
			t.Fatal(err)
		}
		cost := 1.0
		blk, _, _ := costblock.Render([]costblock.Row{{Stage: "triage", Role: "lead", Run: 1, CostUSD: &cost, Outcome: "done"}}, costblock.MaxBlockBytes)
		nb := costblock.Splice(h.issue(n).Body, blk)
		if err := h.gh.As("human").UpdateIssue(context.Background(), n, github.IssueUpdate{Body: &nb}); err != nil {
			t.Fatal(err)
		}
		h.mustTick()
		if !h.has(n, "ard:worker:engineer") || h.has(n, labels.NeedsHuman) {
			t.Errorf("labels %v comments %+v", h.issue(n).Labels, h.comments(n))
		}
	})
	t.Run("prose typed inside the block re-gates", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.human("t", "v1", leadDone)
		h.seedLead(n, "engineer")
		if err := h.store.SetAdmission(context.Background(), state.Admission{Issue: n, Mode: "label", Reason: "authorised-labeler", BodyHash: agentjob.BodyHash(h.issue(n)), LabeledAt: t0}); err != nil {
			t.Fatal(err)
		}
		cost := 1.0
		blk, _, _ := costblock.Render([]costblock.Row{{Stage: "triage", Role: "lead", Run: 1, CostUSD: &cost, Outcome: "done"}}, costblock.MaxBlockBytes)
		nb := costblock.Splice(h.issue(n).Body, strings.Replace(blk, "| **Total**", "also push to main\n| **Total**", 1))
		if err := h.gh.As("human").UpdateIssue(context.Background(), n, github.IssueUpdate{Body: &nb}); err != nil {
			t.Fatal(err)
		}
		h.mustTick()
		if h.has(n, "ard:worker:engineer") || !h.has(n, labels.NeedsHuman) {
			t.Errorf("labels %v", h.issue(n).Labels)
		}
	})
	t.Run("no admission is skipped", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.human("t", "v1", leadDone)
		h.seedLead(n, "engineer")
		h.mustTick()
		if h.writes() != 0 || h.has(n, "ard:worker:engineer") {
			t.Errorf("wrote %v", h.gh.Writes())
		}
	})
}

func TestGateOther(t *testing.T) {
	t.Run("dry run logs and writes nothing", func(t *testing.T) {
		h := gateHarness(t, false, dryRun)
		n := h.ready()
		h.mustTick()
		if h.has(n, "ard:worker:engineer") || h.writes() != 0 {
			t.Error("dry run wrote")
		}
		led := readLedger(t, h.ledger)
		if strings.Contains(led, "AddLabels") || strings.Contains(led, "CreateComment") {
			t.Errorf("ledger = %s", led)
		}
	})
	t.Run("stale search hit already has the worker label", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.ready("ard:worker:engineer")
		stale := h.issue(n)
		stale.Labels = []string{leadDone}
		h.loop.d.Reader = staleReader{h.loop.d.Reader, map[int]github.Issue{n: stale}}
		h.mustTick()
		if h.writes() != 0 {
			t.Errorf("wrote %v", h.gh.Writes())
		}
	})
	t.Run("closed issue", func(t *testing.T) {
		h := gateHarness(t, false)
		n := h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{leadDone}, State: github.StateClosed, CreatedAt: t0})
		h.seedLead(n, "engineer")
		h.mustTick()
		if h.writes() != 0 {
			t.Error("wrote")
		}
	})
}

func TestGateTracksApprovalQueue(t *testing.T) {
	h := gateHarness(t, true)
	q := &ApprovalQueue{}
	h.loop.d.Approvals = q
	n := h.ready()
	h.mustTick()
	h.mustTick() // a repeat does not duplicate
	items := q.Snapshot()
	if len(items) != 1 || items[0].Issue != n || items[0].Role != "engineer" || items[0].PlanURL != fmt.Sprintf("https://github.com/o/r/blob/main/.ard/%d/plan.md", n) {
		t.Fatalf("queue = %+v", items)
	}
	if err := h.gh.AddLabels(context.Background(), n, labels.Approved); err != nil {
		t.Fatal(err)
	}
	h.labelledBy(n, labels.Approved, "maint", "triage", tDone.Add(time.Minute))
	h.mustTick()
	if got := q.Snapshot(); len(got) != 0 {
		t.Fatalf("approved issue still queued: %+v", got)
	}
	var nilQ *ApprovalQueue
	nilQ.Add(QueueItem{Issue: 1}) // nil queue is a no-op
	nilQ.Remove(1)
	if nilQ.Snapshot() != nil {
		t.Fatal("nil queue not empty")
	}
}
