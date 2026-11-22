package reviewer

import (
	"context"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

func TestCandidates(t *testing.T) {
	req := labels.Request(labels.StageReview, reviewer)
	withLabel := func(l string) func(*scenario) {
		return func(s *scenario) { s.pr.Labels = append(s.pr.Labels, l) }
	}
	tests := []struct {
		name string
		mut  []func(*scenario)
		want bool
	}{
		{name: "a plain request", want: true},
		{name: "running", mut: []func(*scenario){withLabel(labels.Running(labels.StageReview, reviewer))}},
		{name: "done", mut: []func(*scenario){withLabel(labels.Done(labels.StageReview, reviewer))}},
		{name: "changes", mut: []func(*scenario){withLabel(labels.Changes(reviewer))}},
		{name: "executor failed", mut: []func(*scenario){withLabel(labels.ExecutorFailed)}},
		{name: "needs human", mut: []func(*scenario){withLabel(labels.NeedsHuman)}},
		{name: "hold", mut: []func(*scenario){withLabel(labels.Hold)}},
		{name: "tests failed", mut: []func(*scenario){withLabel(labels.TestsFailed)}},
		{name: "another role's done label does not exclude", mut: []func(*scenario){withLabel(labels.Done(labels.StageReview, "sec-review"))}, want: true},
		{name: "closed PR", mut: []func(*scenario){func(s *scenario) { s.pr.State = github.StateClosed }}},
		{name: "merged PR", mut: []func(*scenario){func(s *scenario) { s.pr.Merged = true; s.pr.State = github.StateClosed }}},
		{name: "human PR", mut: []func(*scenario){func(s *scenario) { s.pr.Author = "alice" }}},
		{name: "non-worker head", mut: []func(*scenario){func(s *scenario) { s.pr.HeadRef = "feature/x" }}},
		{name: "base not main", mut: []func(*scenario){func(s *scenario) { s.pr.BaseRef = "release" }}},
		{name: "issue closed", mut: []func(*scenario){func(s *scenario) { s.issue.State = github.StateClosed }}},
		{name: "issue held", mut: []func(*scenario){func(s *scenario) { s.issue.Labels = []string{labels.Hold} }}},
		{name: "issue needs human", mut: []func(*scenario){func(s *scenario) { s.issue.Labels = []string{labels.NeedsHuman} }}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			pr, _ := h.scenario(tc.mut...)
			tk := &tick{l: h.loop, cfg: h.cfg, log: quiet, self: self}
			cands, errs := tk.candidates(context.Background())
			if len(errs) != 0 {
				t.Fatal(errs)
			}
			var got []int
			for _, c := range cands {
				if c.role.Name == reviewer {
					got = append(got, c.pr.Number)
				}
			}
			if tc.want != (len(got) == 1 && got[0] == pr) {
				t.Errorf("candidates = %v (PR %d), want %v; request label %s", got, pr, tc.want, req)
			}
		})
	}
}

func TestCandidatesWorkerRunningSkipsButOtherReviewerDoesNot(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	_, issue := h.scenario()
	tk := &tick{l: h.loop, cfg: h.cfg, log: quiet, self: self}
	if _, err := h.store.CreateJob(ctx, state.NewJob{Stage: "review", Role: "sec-review", Issue: issue, PR: 99, Branch: "worker/x-1-y"}); err != nil {
		t.Fatal(err)
	}
	if cands, _ := tk.candidates(ctx); len(cands) != 1 {
		t.Fatalf("a review job of another role must not skip: %d candidates", len(cands))
	}
	if _, err := h.store.CreateJob(ctx, state.NewJob{Stage: "worker", Role: "engineer", Issue: issue}); err != nil {
		t.Fatal(err)
	}
	if cands, _ := tk.candidates(ctx); len(cands) != 0 {
		t.Fatalf("a running worker job must skip: %d candidates", len(cands))
	}
}

func TestCandidatesBothRolesOnOnePR(t *testing.T) {
	h := newHarness(t)
	h.scenario(func(s *scenario) {
		s.pr.Labels = append(s.pr.Labels, labels.Request(labels.StageReview, "sec-review"))
	})
	tk := &tick{l: h.loop, cfg: h.cfg, log: quiet, self: self}
	cands, _ := tk.candidates(context.Background())
	if len(cands) != 2 || cands[0].role.Name != "sec-review" || cands[1].role.Name != reviewer {
		t.Fatalf("candidates = %+v", cands)
	}
}

func TestCandidatesRegateAnEditedHumanIssue(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	_, issue := h.scenario(func(s *scenario) { s.issue.Author = "alice" })
	tk := &tick{l: h.loop, cfg: h.cfg, log: quiet, self: self}
	// Never admitted: skipped quietly.
	if cands, errs := tk.candidates(ctx); len(cands) != 0 || len(errs) != 0 {
		t.Fatalf("unadmitted: %d candidates, %v", len(cands), errs)
	}
	is, _ := h.gh.GetIssue(ctx, issue)
	if err := h.store.SetAdmission(ctx, state.Admission{Issue: issue, Mode: "label", Reason: "authorised", LabeledAt: t0, BodyHash: strings.Repeat("ab", 32)}); err != nil {
		t.Fatal(err)
	}
	tk.l = h.loop
	if cands, errs := tk.candidates(ctx); len(cands) != 0 || len(errs) != 0 {
		t.Fatalf("edited: %d candidates, %v", len(cands), errs)
	}
	cs, _ := h.gh.ListComments(ctx, issue, is.CreatedAt)
	if len(cs) != 1 || !h.has(issue, labels.NeedsHuman) {
		t.Errorf("want a regate comment and needs-human on the issue: %v", cs)
	}
}
