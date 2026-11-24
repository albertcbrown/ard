package merger

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/gitx"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/reviewfix"
	"github.com/albertcbrown/ard/internal/state"
)

type fakeRounds struct{ reqs []reviewfix.Request }

func (f *fakeRounds) RequestRound(_ context.Context, r reviewfix.Request) (bool, error) {
	f.reqs = append(f.reqs, r)
	return true, nil
}

// withRounds configures the engineer worker role and the Rounds dependency.
func withRounds(h *harness, r Rounds, max int) {
	h.cfg.Roles = append(h.cfg.Roles, config.Role{Kind: config.KindWorker, Name: "engineer", MaxReviewRounds: max})
	h.loop.d.Rounds = r
}

func (h *harness) seedRound(issue int, phase string, head string) {
	h.t.Helper()
	data, _ := json.Marshal(reviewfix.Round{N: 1, Phase: phase, Reason: reviewfix.ReasonConflict, PR: 1, Head: head, Worker: "engineer"})
	if err := h.store.AppendEvent(context.Background(), state.Event{Kind: state.EventReviewRound, Issue: issue, Role: "engineer", Data: data}); err != nil {
		h.t.Fatal(err)
	}
}

func TestConflictRequestsRound(t *testing.T) {
	h := newHarness(t)
	fr := &fakeRounds{}
	withRounds(h, fr, 3)
	p := h.newPR(func(s *spec) { s.mergeSt = "dirty" })
	h.mustTick()
	if len(fr.reqs) != 1 {
		t.Fatalf("requests = %+v", fr.reqs)
	}
	r := fr.reqs[0]
	if r.Reason != reviewfix.ReasonConflict || r.Issue != p.issue || r.Worker != "engineer" || r.Max != 3 || len(r.Roles) != 1 || r.Roles[0] != reviewer {
		t.Errorf("request = %+v", r)
	}
	if h.has(p.n, labels.NeedsHuman) || len(h.comments(p.n)) != 0 || h.merged(p.n) {
		t.Fatal("a requested conflict round must not label or comment")
	}
}

func TestConflictNilRoundsNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.cfg.Roles = append(h.cfg.Roles, config.Role{Kind: config.KindWorker, Name: "engineer", MaxReviewRounds: 3})
	p := h.newPR(func(s *spec) { s.mergeSt = "dirty" })
	h.mustTick()
	if !h.has(p.n, labels.NeedsHuman) || len(h.comments(p.n)) != 1 {
		t.Fatal("nil Rounds must keep needs-human")
	}
}

func TestConflictUnknownWorkerNeedsHuman(t *testing.T) {
	h := newHarness(t)
	h.loop.d.Rounds = &fakeRounds{} // no worker role configured
	p := h.newPR(func(s *spec) { s.mergeSt = "dirty" })
	h.mustTick()
	if !h.has(p.n, labels.NeedsHuman) {
		t.Fatal("unconfigured worker must fall back to needs-human")
	}
}

func TestConflictPendingAndCapped(t *testing.T) {
	for _, phase := range []string{reviewfix.PhaseRequested, reviewfix.PhaseCapped} {
		t.Run(phase, func(t *testing.T) {
			h := newHarness(t)
			fr := &fakeRounds{}
			withRounds(h, fr, 3)
			p := h.newPR(func(s *spec) { s.mergeSt = "dirty" })
			h.seedRound(p.issue, phase, p.head)
			h.mustTick()
			if len(fr.reqs) != 0 || h.has(p.n, labels.NeedsHuman) || len(h.comments(p.n)) != 0 {
				t.Fatalf("requests %d labels %v", len(fr.reqs), h.labelsOf(p.n))
			}
		})
	}
}

func TestConflictDryRun(t *testing.T) {
	h := newHarness(t, dryRun)
	fr := &fakeRounds{}
	withRounds(h, fr, 3)
	p := h.newPR(func(s *spec) { s.mergeSt = "dirty" })
	out := h.mustTick()
	if len(fr.reqs) != 0 || h.has(p.n, labels.NeedsHuman) || !strings.Contains(out, "merge.would_request_conflict_round") {
		t.Fatalf("requests %d\n%s", len(fr.reqs), out)
	}
}

func TestConflictUnknownRetries(t *testing.T) {
	h := newHarness(t)
	fr := &fakeRounds{}
	withRounds(h, fr, 3)
	p := h.newPR(func(s *spec) { s.mergeSt = "unknown" })
	h.mustTick()
	if len(fr.reqs) != 0 || h.has(p.n, labels.NeedsHuman) {
		t.Fatal("unknown mergeability must retry without a round")
	}
}

func TestUpdateBranchConflictRequestsRound(t *testing.T) {
	h := newHarness(t)
	fr := &fakeRounds{}
	withRounds(h, fr, 3)
	p := h.newPR()
	h.commit("main", "main_only.go", "package m\n")
	h.loop.d.Effects = flipEffects{h.fx, h}
	h.mustTick()
	if len(fr.reqs) != 1 || fr.reqs[0].Reason != reviewfix.ReasonConflict || h.has(p.n, labels.NeedsHuman) {
		t.Fatalf("requests %+v labels %v", fr.reqs, h.labelsOf(p.n))
	}
}

// With the real review fix loop, a conflict round counts toward the same cap
// as review rounds and the cap comment and event say conflict.
func TestConflictCountsWithReviewRounds(t *testing.T) {
	h := newHarness(t)
	rf := reviewfix.New(reviewfix.Deps{Reader: h.gh, Effects: h.fx, Store: h.store, Git: gitx.New(""), RepoDir: h.clone, RemoteURL: h.bare, Log: quiet})
	withRounds(h, rf, 2)
	p := h.newPR(func(s *spec) { s.mergeSt = "dirty" })
	data, _ := json.Marshal(reviewfix.Round{N: 1, Phase: reviewfix.PhaseRequested, Reason: reviewfix.ReasonReview, PR: p.n, Head: "old", Worker: "engineer"})
	if err := h.store.AppendEvent(t.Context(), state.Event{Kind: state.EventReviewRound, Issue: p.issue, Role: "engineer", Data: data}); err != nil {
		t.Fatal(err)
	}
	h.mustTick() // one review round plus this conflict round: 2 of 2
	rs, err := reviewfix.Rounds(t.Context(), h.store, p.issue)
	if err != nil || len(rs) != 2 || rs[1].Reason != reviewfix.ReasonConflict || rs[1].Phase != reviewfix.PhaseRequested {
		t.Fatalf("rounds = %+v, %v", rs, err)
	}
	h.mustTick() // pending at this head: nothing new
	if rs, _ = reviewfix.Rounds(t.Context(), h.store, p.issue); len(rs) != 2 {
		t.Fatalf("rounds = %+v", rs)
	}
}

func TestConflictCapReached(t *testing.T) {
	h := newHarness(t)
	rf := reviewfix.New(reviewfix.Deps{Reader: h.gh, Effects: h.fx, Store: h.store, Git: gitx.New(""), RepoDir: h.clone, RemoteURL: h.bare, Log: quiet})
	withRounds(h, rf, 1)
	p := h.newPR(func(s *spec) { s.mergeSt = "dirty" })
	data, _ := json.Marshal(reviewfix.Round{N: 1, Phase: reviewfix.PhaseRequested, Reason: reviewfix.ReasonReview, PR: p.n, Head: "old", Worker: "engineer"})
	if err := h.store.AppendEvent(t.Context(), state.Event{Kind: state.EventReviewRound, Issue: p.issue, Role: "engineer", Data: data}); err != nil {
		t.Fatal(err)
	}
	h.mustTick()
	rs, _ := reviewfix.Rounds(t.Context(), h.store, p.issue)
	last := rs[len(rs)-1]
	if last.Phase != reviewfix.PhaseCapped || last.Reason != reviewfix.ReasonConflict || !h.has(p.issue, labels.NeedsHuman) {
		t.Fatalf("rounds = %+v", rs)
	}
	cs := h.comments(p.issue)
	if len(cs) != 1 || !strings.Contains(cs[0].Body, "Conflict rounds stopped") {
		t.Errorf("comments = %+v", cs)
	}
}
