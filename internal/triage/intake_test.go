package triage

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

func (h *harness) enabledAtMeta() string {
	v, err := h.store.Meta(context.Background(), metaEnabledAt)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		h.t.Fatal(err)
	}
	return v
}

func TestEnabledAt(t *testing.T) {
	t.Run("the first all-mode tick stores the newest GitHub timestamp", func(t *testing.T) {
		h := newHarness(t, allMode)
		h.gh.SeedIssue(github.Issue{Title: "old", Author: "a", CreatedAt: t0, UpdatedAt: t0})
		h.gh.SeedIssue(github.Issue{Title: "newer", Author: "a", CreatedAt: t0.Add(time.Hour), UpdatedAt: t0.Add(3 * time.Hour)})
		h.mustTick()
		if got, want := h.enabledAtMeta(), strconv.FormatInt(t0.Add(3*time.Hour).Unix(), 10); got != want {
			t.Errorf("enabled at %q, want %q", got, want)
		}
		// Later issues do not move it.
		h.gh.SeedIssue(github.Issue{Title: "later", Author: "a", CreatedAt: t0.Add(9 * time.Hour)})
		h.mustTick()
		if got, want := h.enabledAtMeta(), strconv.FormatInt(t0.Add(3*time.Hour).Unix(), 10); got != want {
			t.Errorf("enabled at moved to %q", got)
		}
	})
	t.Run("an empty repo stores nothing and nothing is admitted", func(t *testing.T) {
		h := newHarness(t, allMode)
		h.mustTick()
		if h.enabledAtMeta() != "" {
			t.Error("stored")
		}
	})
	t.Run("label mode clears it", func(t *testing.T) {
		h := newHarness(t, allMode)
		h.gh.SeedIssue(github.Issue{Title: "x", Author: "a", CreatedAt: t0})
		h.mustTick()
		if h.enabledAtMeta() == "" {
			t.Fatal("not stored")
		}
		h.cfg.Intake.Mode = "label"
		h.mustTick()
		if h.enabledAtMeta() != "" {
			t.Error("not cleared")
		}
	})
	t.Run("an issue created exactly at EnabledAt is not admitted, one second later it is", func(t *testing.T) {
		h := newHarness(t, allMode)
		first := h.human("first", "b")
		h.mustTick() // EnabledAt = first's time
		h.mustTick()
		if h.has(first, lead) {
			t.Error("admitted at the enable time")
		}
		second := h.gh.SeedIssue(github.Issue{Title: "second", Author: "alice", CreatedAt: t0.Add(time.Second)})
		h.mustTick()
		if !h.has(second, lead) {
			t.Error("not admitted one second later")
		}
	})
}

func TestAllModeAdmitsNewIssues(t *testing.T) {
	h := newHarness(t, allMode)
	status := h.gh.SeedIssue(github.Issue{Title: "ard status", Author: self, CreatedAt: t0})
	backlog := h.gh.SeedIssue(github.Issue{Title: "backlog", Author: "alice", CreatedAt: t0.Add(-time.Hour)})
	h.mustTick()
	n := h.gh.SeedIssue(github.Issue{Title: "new", Body: "b", Author: "alice", CreatedAt: t0.Add(time.Hour)})
	h.mustTick()
	if !h.has(n, lead) {
		t.Fatal("the new issue got no ard:pending")
	}
	if h.has(backlog, lead) {
		t.Error("a backlog issue was admitted")
	}
	if h.has(status, lead) || len(h.issue(status).Labels) != 0 {
		t.Error("the status issue was swept")
	}
	if len(h.ex.prepared) != 1 || h.ex.prepared[0].Issue != n {
		t.Fatalf("prepared = %+v", h.ex.prepared)
	}
	a, err := h.store.Admission(context.Background(), n)
	if err != nil || a.Reason != "all-mode" || a.Mode != "all" || a.LabeledBy != "" {
		t.Errorf("admission = %+v, %v", a, err)
	}
	// A labelled backlog issue still goes through the label rules.
	h.gh.SeedTimeline(backlog, github.TimelineEvent{Event: "labeled", Label: lead, Actor: "maint", CreatedAt: t0.Add(2 * time.Hour)})
	h.gh.SetPermission("maint", github.Permission{Role: "triage"})
	if err := h.gh.As("maint").AddLabels(context.Background(), backlog, lead); err != nil {
		t.Fatal(err)
	}
	h.mustTick()
	if len(h.ex.prepared) != 2 {
		t.Errorf("a maintainer-labelled backlog issue was not claimed: %d", len(h.ex.prepared))
	}
}

func TestLabelModeIntake(t *testing.T) {
	t.Run("an authorised labeler admits", func(t *testing.T) {
		h := newHarness(t)
		n := h.human("t", "b", lead)
		h.labelledBy(n, lead, "maint", "triage", t0.Add(time.Hour))
		h.mustTick()
		if len(h.ex.prepared) != 1 {
			t.Fatal("not claimed")
		}
	})
	t.Run("a read-only labeler is refused once", func(t *testing.T) {
		h := newHarness(t)
		n := h.human("SECRET-TITLE", "SECRET-BODY", lead)
		h.labelledBy(n, lead, "rando", "read", t0.Add(time.Hour))
		h.mustTick()
		if len(h.ex.prepared) != 0 || !h.has(n, labels.NeedsHuman) {
			t.Fatalf("prepared %d, labels %v", len(h.ex.prepared), h.issue(n).Labels)
		}
		cs := h.comments(n)
		if len(cs) != 1 || !strings.HasPrefix(cs[0].Body, "<!-- "+labels.Prefix+"intake refuse label="+lead+" at=") {
			t.Fatalf("comments = %+v", cs)
		}
		for _, bad := range []string{"@", "rando", "SECRET"} {
			if strings.Contains(cs[0].Body, bad) {
				t.Errorf("the comment contains %q", bad)
			}
		}
		// A replay (a human removed ard:needs-human without relabelling) re-adds the label but not the comment.
		if err := h.gh.RemoveLabel(context.Background(), n, labels.NeedsHuman); err != nil {
			t.Fatal(err)
		}
		h.mustTick()
		if len(h.comments(n)) != 1 || !h.has(n, labels.NeedsHuman) {
			t.Errorf("replay: %d comments, needs-human %v", len(h.comments(n)), h.has(n, labels.NeedsHuman))
		}
		// A fresh relabel by another outsider gets a new comment.
		if err := h.gh.RemoveLabel(context.Background(), n, labels.NeedsHuman); err != nil {
			t.Fatal(err)
		}
		h.labelledBy(n, lead, "other", "read", t0.Add(5*time.Hour))
		h.mustTick()
		if len(h.comments(n)) != 2 {
			t.Errorf("comments = %d, want 2", len(h.comments(n)))
		}
	})
	t.Run("a permission error means no decision and no write", func(t *testing.T) {
		h := newHarness(t)
		n := h.human("t", "b", lead)
		h.labelledBy(n, lead, "maint", "write", t0.Add(time.Hour))
		h.gh.FailNext("GetPermission", errors.New("boom"))
		if err := h.tick(); err == nil {
			t.Error("the tick should report the error")
		}
		if len(h.gh.Writes()) != 0 || len(h.ex.prepared) != 0 {
			t.Errorf("acted: %v", h.gh.Writes())
		}
		h.mustTick() // the next poll succeeds
		if len(h.ex.prepared) != 1 {
			t.Error("not claimed on the next poll")
		}
	})
	t.Run("a template-applied label with no timeline event is not admitted", func(t *testing.T) {
		h := newHarness(t)
		h.human("t", "b", lead)
		h.mustTick()
		if len(h.ex.prepared) != 0 || len(h.gh.Writes()) != 0 {
			t.Error("acted")
		}
	})
}

func TestCustomIntakeLabel(t *testing.T) {
	const intakeLabel = "ard:intake"
	h := newHarness(t, withLabel(intakeLabel))
	n := h.human("t", "b", intakeLabel)
	h.labelledBy(n, intakeLabel, "maint", "maintain", t0.Add(time.Hour))
	tmpl := h.human("template", "b", lead) // ard:pending from an issue template, no intake label
	status := h.gh.SeedIssue(github.Issue{Title: "status", Author: self, CreatedAt: t0})
	h.mustTick()
	if !h.has(n, lead) {
		t.Fatal("the authorised intake label did not add ard:pending")
	}
	if len(h.ex.prepared) != 1 || h.ex.prepared[0].Issue != n {
		t.Fatalf("prepared = %+v", h.ex.prepared)
	}
	if h.has(tmpl, labels.Running(labels.StageTriage, "lead")) || len(h.issue(status).Labels) != 0 {
		t.Error("the template issue or the status issue was touched")
	}
	// An outsider's intake label is refused.
	m := h.human("t2", "b", intakeLabel)
	h.labelledBy(m, intakeLabel, "rando", "read", t0.Add(time.Hour))
	h.mustTick()
	if !h.has(m, labels.NeedsHuman) || h.has(m, lead) {
		t.Errorf("labels = %v", h.issue(m).Labels)
	}
}

// Editing the issue after it was admitted re-gates every later triage role until
// a maintainer re-applies the label.
func TestRegateAfterBodyEdit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	n := h.human("t", "original", lead)
	h.labelledBy(n, lead, "maint", "maintain", t0.Add(time.Hour))
	h.mustTick()
	if len(h.ex.prepared) != 1 || h.ex.prepared[0].Role != "lead" {
		t.Fatal("lead not claimed")
	}
	// The lead finished: close-out removed ard:pending and :running, added :done and requested architect.
	job := h.jobs()[0]
	if err := h.store.Complete(ctx, job.ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{lead, labels.Running(labels.StageTriage, "lead")} {
		_ = h.gh.RemoveLabel(ctx, n, l)
	}
	if err := h.gh.AddLabels(ctx, n, labels.Done(labels.StageTriage, "lead"), labels.Request(labels.StageTriage, "architect")); err != nil {
		t.Fatal(err)
	}
	h.mustTick()
	if len(h.ex.prepared) != 2 || h.ex.prepared[1].Role != "architect" {
		t.Fatalf("architect not claimed with an unchanged body: %+v", h.ex.prepared)
	}

	// A second issue: lead runs, then the body is edited before architect.
	m := h.human("t", "original", lead)
	h.labelledBy(m, lead, "maint", "maintain", t0.Add(time.Hour))
	h.mustTick()
	if err := h.store.Complete(ctx, h.jobsOf(m)[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{lead, labels.Running(labels.StageTriage, "lead")} {
		_ = h.gh.RemoveLabel(ctx, m, l)
	}
	if err := h.gh.AddLabels(ctx, m, labels.Done(labels.StageTriage, "lead"), labels.Request(labels.StageTriage, "architect")); err != nil {
		t.Fatal(err)
	}
	body := "edited after admission"
	if err := h.gh.UpdateIssue(ctx, m, github.IssueUpdate{Body: &body}); err != nil {
		t.Fatal(err)
	}
	before := len(h.ex.prepared)
	h.mustTick()
	if len(h.ex.prepared) != before {
		t.Fatal("architect was claimed after the body changed")
	}
	cs := h.comments(m)
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "regate body=") || !h.has(m, labels.NeedsHuman) {
		t.Fatalf("comments %+v labels %v", cs, h.issue(m).Labels)
	}
	h.mustTick()
	if len(h.comments(m)) != 1 {
		t.Error("a second comment")
	}

	// A maintainer reviews, removes ard:needs-human and re-applies the label.
	if err := h.gh.RemoveLabel(ctx, m, labels.NeedsHuman); err != nil {
		t.Fatal(err)
	}
	if err := h.gh.As("maint").AddLabels(ctx, m, lead); err != nil {
		t.Fatal(err)
	}
	h.mustTick()
	if len(h.ex.prepared) != before+1 || h.ex.prepared[before].Role != "architect" || h.ex.prepared[before].Issue != m {
		t.Fatalf("architect not claimed after the relabel: %+v", h.ex.prepared[before:])
	}
	a, err := h.store.Admission(ctx, m)
	if err != nil || a.BodyHash != BodyHash(h.issue(m)) {
		t.Errorf("admission = %+v, %v", a, err)
	}
}

func (h *harness) jobsOf(issue int) []state.Job {
	var out []state.Job
	for _, j := range h.jobs() {
		if j.Issue == issue {
			out = append(out, j)
		}
	}
	return out
}

// In all mode the sweep's own ard:pending must not defeat the re-gate.
func TestRegateInAllModeNeedsAHumanLabel(t *testing.T) {
	h := newHarness(t, allMode)
	ctx := context.Background()
	h.gh.SeedIssue(github.Issue{Title: "status", Author: self, CreatedAt: t0})
	h.mustTick()
	n := h.gh.SeedIssue(github.Issue{Title: "new", Body: "v1", Author: "alice", CreatedAt: t0.Add(time.Hour)})
	h.mustTick()
	if len(h.ex.prepared) != 1 {
		t.Fatal("lead not claimed")
	}
	if err := h.store.Complete(ctx, h.jobs()[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{lead, labels.Running(labels.StageTriage, "lead")} {
		_ = h.gh.RemoveLabel(ctx, n, l)
	}
	if err := h.gh.AddLabels(ctx, n, labels.Done(labels.StageTriage, "lead"), labels.Request(labels.StageTriage, "architect")); err != nil {
		t.Fatal(err)
	}
	body := "v2"
	if err := h.gh.UpdateIssue(ctx, n, github.IssueUpdate{Body: &body}); err != nil {
		t.Fatal(err)
	}
	h.mustTick()
	if len(h.ex.prepared) != 1 || !h.has(n, labels.NeedsHuman) {
		t.Fatalf("not re-gated: prepared %d labels %v", len(h.ex.prepared), h.issue(n).Labels)
	}
}

// A relabel that predates the edit does not re-admit it; only one after the
// re-gate does.
func TestRelabelBeforeEditStaysRegated(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	m := h.human("t", "original", lead)
	h.labelledBy(m, lead, "maint", "maintain", t0.Add(time.Hour))
	h.mustTick()
	if err := h.store.Complete(ctx, h.jobsOf(m)[0].ID, nil); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{lead, labels.Running(labels.StageTriage, "lead")} {
		_ = h.gh.RemoveLabel(ctx, m, l)
	}
	if err := h.gh.AddLabels(ctx, m, labels.Done(labels.StageTriage, "lead"), labels.Request(labels.StageTriage, "architect")); err != nil {
		t.Fatal(err)
	}
	// The maintainer relabels first, then the author edits.
	h.labelledBy(m, lead, "maint", "maintain", t0.Add(2*time.Hour))
	body := "edited after the relabel"
	if err := h.gh.UpdateIssue(ctx, m, github.IssueUpdate{Body: &body}); err != nil {
		t.Fatal(err)
	}
	before := len(h.ex.prepared)
	h.mustTick()
	if len(h.ex.prepared) != before || !h.has(m, labels.NeedsHuman) {
		t.Fatalf("not re-gated: prepared %d labels %v", len(h.ex.prepared), h.issue(m).Labels)
	}
	a, err := h.store.Admission(ctx, m)
	if err != nil || a.RegatedAt.IsZero() || a.RegatedHash != BodyHash(h.issue(m)) {
		t.Errorf("admission = %+v, %v", a, err)
	}
}
