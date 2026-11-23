package worker

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

func (h *harness) human(title, body string, lbls ...string) int {
	base := []string{labels.Request(labels.StageWorker, "engineer"), labels.Approved}
	return h.gh.SeedIssue(github.Issue{Title: title, Body: body, Author: "alice", Labels: append(base, lbls...), CreatedAt: t0, UpdatedAt: t0})
}

func (h *harness) admit(n int, hash string) {
	h.t.Helper()
	if err := h.store.SetAdmission(context.Background(), state.Admission{Issue: n, Mode: "label", Reason: "authorised-labeler", LabeledBy: "maint", BodyHash: hash, LabeledAt: t0}); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) claimed() bool { return len(h.ex.ids()) > 0 }

func TestCandidates(t *testing.T) {
	run := func(t *testing.T, setup func(h *harness) int) *harness {
		h := newHarness(t, noTests)
		setup(h)
		h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
		h.mustTick()
		return h
	}
	for name, tc := range map[string]struct {
		setup func(h *harness) int
		want  bool
	}{
		"app-authored passes without a record": {func(h *harness) int { return h.issue("t") }, true},
		"excluded: running": {func(h *harness) int {
			return h.issue("t", labels.Running(labels.StageWorker, "engineer"))
		}, false},
		"excluded: done":            {func(h *harness) int { return h.issue("t", labels.Done(labels.StageWorker, "engineer")) }, false},
		"excluded: executor failed": {func(h *harness) int { return h.issue("t", labels.ExecutorFailed) }, false},
		"excluded: needs-human":     {func(h *harness) int { return h.issue("t", labels.NeedsHuman) }, false},
		"excluded: hold":            {func(h *harness) int { return h.issue("t", labels.Hold) }, false},
		"approval required, missing": {func(h *harness) int {
			return h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{labels.Request(labels.StageWorker, "engineer")}, CreatedAt: t0})
		}, false},
		"approval not required": {func(h *harness) int {
			h.cfg.RequireApproval = false
			return h.gh.SeedIssue(github.Issue{Title: "t", Author: self, Labels: []string{labels.Request(labels.StageWorker, "engineer")}, CreatedAt: t0})
		}, true},
		"human issue, no record": {func(h *harness) int { return h.human("t", "b") }, false},
		"human issue, equal hash": {func(h *harness) int {
			n := h.human("t", "b")
			h.admit(n, agentjob.BodyHash(h.getIssue(n)))
			return n
		}, true},
		"running triage job": {func(h *harness) int {
			n := h.issue("t")
			if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "triage", Role: "lead", Issue: n}); err != nil {
				t.Fatal(err)
			}
			return n
		}, false},
		"running review job": {func(h *harness) int {
			n := h.issue("t")
			if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "review", Role: "eng-review", Issue: n, PR: 99, Branch: "b"}); err != nil {
				t.Fatal(err)
			}
			return n
		}, false},
		"running worker job of another role": {func(h *harness) int {
			n := h.issue("t")
			if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "worker", Role: "other", Issue: n}); err != nil {
				t.Fatal(err)
			}
			return n
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			h := run(t, tc.setup)
			if h.claimed() != tc.want {
				t.Errorf("claimed = %v, want %v", h.claimed(), tc.want)
			}
		})
	}
	t.Run("closed issue", func(t *testing.T) {
		h := newHarness(t, noTests)
		h.gh.SeedIssue(github.Issue{Title: "t", Author: self, State: github.StateClosed, Labels: []string{labels.Request(labels.StageWorker, "engineer"), labels.Approved}, CreatedAt: t0})
		h.mustTick()
		if h.claimed() {
			t.Error("claimed a closed issue")
		}
	})
}

func TestChangedBodyIsRegatedOnce(t *testing.T) {
	h := newHarness(t, noTests)
	n := h.human("t", "edited body")
	h.admit(n, agentjob.BodyHash(github.Issue{Title: "t", Body: "original body"}))
	h.mustTick()
	h.mustTick()
	if h.claimed() || !h.has(n, labels.NeedsHuman) {
		t.Fatal("a changed issue must be parked")
	}
	cs := h.comments(n)
	if len(cs) != 1 || !strings.HasPrefix(cs[0].Body, "<!-- ard:worker regate body=") || !strings.Contains(cs[0].Body, labels.Pending) {
		t.Errorf("comments = %+v", cs)
	}
	a, err := h.store.Admission(context.Background(), n)
	if err != nil || a.RegatedHash != agentjob.BodyHash(h.getIssue(n)) {
		t.Errorf("admission = %+v, %v", a, err)
	}
}

func TestDryRunClaimsNothing(t *testing.T) {
	h := newHarness(t, noTests, dryRun)
	n := h.issue("t")
	h.agentWrites(map[string]string{"a.txt": "x"}, roleDone, nil)
	h.mustTick()
	if h.claimed() || len(h.jobs()) != 0 || h.has(n, labels.Running(labels.StageWorker, "engineer")) || len(h.prs()) != 0 {
		t.Error("dry-run claimed something")
	}
	if h.pool.offers != 1 || len(h.pool.last) != 0 {
		t.Errorf("offers %d last %v", h.pool.offers, h.pool.last)
	}
	if b, _ := os.ReadFile(h.ledger); len(b) != 0 {
		t.Errorf("ledger = %s", b)
	}
}

func TestNoWorkerRolesOffersNothing(t *testing.T) {
	h := newHarness(t)
	h.cfg.Roles = h.cfg.Roles[1:]
	h.issue("t")
	h.mustTick()
	if h.pool.offers != 1 || len(h.pool.last) != 0 || h.claimed() {
		t.Errorf("offers %d last %v", h.pool.offers, h.pool.last)
	}
}
