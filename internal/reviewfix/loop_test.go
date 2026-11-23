package reviewfix

import (
	"errors"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
)

func cat(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestHandle(t *testing.T) {
	issueDefault := []string{lblWorkerReq, lblWorkerDone}
	prDefault := cat(reqChanges(engRev), reqDone(secRev))
	tests := []struct {
		name        string
		issueLbl    []string
		prLbl       []string
		prMut       func(*github.PullRequest)
		noRecord    bool
		otherRec    bool     // a changes record only for sec-review
		rounds      []string // seeded phases, in order
		cappedHere  bool     // the seeded capped round is at the current head
		claim       string   // "", "done", "running"
		runReview   bool
		changed     []string
		max         int
		dry         bool
		closedIssue bool

		wantPhases   string
		wantComments int
		issueGone    []string // labels that must be gone from the issue
		issueHas     []string
		prGone       []string
		prHas        []string
	}{
		{name: "changes starts round 1", wantPhases: "requested", issueGone: []string{lblWorkerDone}, issueHas: []string{lblWorkerReq}},
		{name: "changes label without record", noRecord: true, wantPhases: "", issueHas: []string{lblWorkerDone}},
		{name: "another reviewer running", prLbl: cat(reqChanges(engRev), []string{labels.Request(labels.StageReview, secRev), labels.Running(labels.StageReview, secRev)}), issueHas: []string{lblWorkerDone}},
		{name: "another reviewer pending", prLbl: cat(reqChanges(engRev), []string{labels.Request(labels.StageReview, secRev)}), issueHas: []string{lblWorkerDone}},
		{name: "review job running", runReview: true, issueHas: []string{lblWorkerDone}},
		{name: "lost removal is replayed", rounds: []string{PhaseRequested}, wantPhases: "requested", issueGone: []string{lblWorkerDone}},
		{name: "worker pending", rounds: []string{PhaseRequested}, issueLbl: []string{lblWorkerReq}, wantPhases: "requested"},
		{name: "worker running", rounds: []string{PhaseRequested}, claim: "running", issueLbl: []string{lblWorkerReq, labels.Running(labels.StageWorker, wrk)}, wantPhases: "requested"},
		{name: "claim without done label", rounds: []string{PhaseRequested}, claim: "done", issueLbl: []string{lblWorkerReq}, wantPhases: "requested"},
		{name: "fix landed with code", rounds: []string{PhaseRequested}, claim: "done", changed: []string{"hello.go"},
			wantPhases: "requested,applied", prGone: cat(reqChanges(engRev)[1:], reqDone(secRev)[1:]), prHas: []string{labels.Request(labels.StageReview, engRev), labels.Request(labels.StageReview, secRev)}},
		{name: "fix landed without code", rounds: []string{PhaseRequested}, claim: "done", wantPhases: "requested,capped", wantComments: 1,
			issueHas: []string{labels.NeedsHuman}, prHas: []string{labels.Changes(engRev), labels.Done(labels.StageReview, secRev)}},
		{name: "cap reached", max: 2, rounds: []string{PhaseRequested, PhaseApplied, PhaseRequested, PhaseApplied}, wantPhases: "requested,applied,requested,applied,capped",
			wantComments: 1, issueHas: []string{labels.NeedsHuman, lblWorkerDone}},
		{name: "cap cleared by a person", max: 1, rounds: []string{PhaseRequested, PhaseApplied, PhaseCapped}, cappedHere: true, wantPhases: "requested,applied,capped"},
		{name: "new head after a cleared cap", max: 1, rounds: []string{PhaseRequested, PhaseApplied, PhaseCapped}, wantPhases: "requested,applied,capped,capped", wantComments: 1, issueHas: []string{labels.NeedsHuman}},
		{name: "foreign author", prMut: func(p *github.PullRequest) { p.Author = "human" }},
		{name: "base not main", prMut: func(p *github.PullRequest) { p.BaseRef = "dev" }},
		{name: "not a worker branch", prMut: func(p *github.PullRequest) { p.HeadRef = "feature/x" }},
		{name: "pr on hold", prLbl: cat(reqChanges(engRev), []string{labels.Hold})},
		{name: "tests failed", issueLbl: []string{lblWorkerReq, lblWorkerDone, labels.TestsFailed}},
		{name: "executor failed", issueLbl: []string{lblWorkerReq, lblWorkerDone, labels.ExecutorFailed}},
		{name: "issue closed", closedIssue: true},
		{name: "dry run", dry: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			maxR := tc.max
			if maxR == 0 {
				maxR = 3
			}
			h := newHarness(t, maxR, tc.dry)
			issueLbl := tc.issueLbl
			if issueLbl == nil {
				issueLbl = issueDefault
			}
			prLbl := tc.prLbl
			if prLbl == nil {
				prLbl = prDefault
			}
			s := h.seed(issueLbl, prLbl, tc.prMut)
			if tc.closedIssue {
				closed := github.StateClosed
				if err := h.gh.UpdateIssue(t.Context(), s.issue, github.IssueUpdate{State: &closed}); err != nil {
					t.Fatal(err)
				}
			}
			if !tc.noRecord {
				h.closeoutChanges(s, engRev)
			}
			n := 0
			for _, ph := range tc.rounds {
				head := "h1"
				if ph == PhaseCapped && tc.cappedHere {
					head = "h2"
				}
				if ph == PhaseRequested {
					n++
				}
				h.round(s, max(n, 1), ph, head)
			}
			switch tc.claim {
			case "done":
				h.workerJob(s, true)
			case "running":
				h.workerJob(s, false)
			}
			if tc.runReview {
				if _, err := h.store.CreateJob(t.Context(), stateJob("review", engRev, s)); err != nil {
					t.Fatal(err)
				}
			}
			h.git.changed = tc.changed
			before := len(h.gh.Writes())
			if err := h.tick(); err != nil {
				t.Fatal(err)
			}
			if got := h.phases(s.issue); got != tc.wantPhases {
				t.Errorf("phases = %q, want %q", got, tc.wantPhases)
			}
			if tc.wantPhases == "" && len(h.gh.Writes()) != before {
				t.Errorf("unexpected writes: %v", h.gh.Writes()[before:])
			}
			if got := h.comments(s.issue); got != tc.wantComments {
				t.Errorf("comments = %d, want %d", got, tc.wantComments)
			}
			for _, l := range tc.issueGone {
				if h.has(s.issue, l) {
					t.Errorf("issue still has %s", l)
				}
			}
			for _, l := range tc.issueHas {
				if !h.has(s.issue, l) {
					t.Errorf("issue lost %s", l)
				}
			}
			for _, l := range tc.prGone {
				if h.has(s.pr, l) {
					t.Errorf("pr still has %s", l)
				}
			}
			for _, l := range tc.prHas {
				if !h.has(s.pr, l) {
					t.Errorf("pr lost %s", l)
				}
			}
		})
	}
}

func TestRoundCarriesDetails(t *testing.T) {
	h := newHarness(t, 3, false)
	s := h.seed([]string{lblWorkerReq, lblWorkerDone}, reqChanges(engRev), nil)
	h.closeoutChanges(s, engRev)
	if err := h.tick(); err != nil {
		t.Fatal(err)
	}
	rs, err := Rounds(t.Context(), h.store, s.issue)
	if err != nil || len(rs) != 1 {
		t.Fatalf("rounds = %v, %v", rs, err)
	}
	r := rs[0]
	if r.N != 1 || r.PR != s.pr || r.Head != "h2" || r.Worker != wrk || r.Reason != ReasonReview || len(r.Roles) != 1 || r.Roles[0] != engRev || r.ID == 0 {
		t.Errorf("round = %+v", r)
	}
}

func TestTwoTicksOneRound(t *testing.T) {
	h := newHarness(t, 3, false)
	s := h.seed([]string{lblWorkerReq, lblWorkerDone}, reqChanges(engRev), nil)
	h.closeoutChanges(s, engRev)
	for range 3 {
		if err := h.tick(); err != nil {
			t.Fatal(err)
		}
	}
	if got := h.phases(s.issue); got != "requested" {
		t.Errorf("phases = %q, want one requested round", got)
	}
}

func TestOneFailureDoesNotStopTheTick(t *testing.T) {
	h := newHarness(t, 3, false)
	a := h.seed([]string{lblWorkerReq, lblWorkerDone}, reqChanges(engRev), nil)
	b := h.seed([]string{lblWorkerReq, lblWorkerDone}, reqChanges(engRev), nil)
	h.closeoutChanges(a, engRev)
	h.closeoutChanges(b, engRev)
	h.gh.FailNext("RemoveLabel", errors.New("boom"))
	if err := h.tick(); err == nil {
		t.Fatal("tick returned nil, want the failed label removal")
	}
	if h.phases(a.issue) != "requested" || h.phases(b.issue) != "requested" {
		t.Errorf("phases a=%q b=%q, want both handled", h.phases(a.issue), h.phases(b.issue))
	}
	if h.has(b.issue, lblWorkerDone) && h.has(a.issue, lblWorkerDone) {
		t.Error("the second PR was not handled")
	}
	// The next tick replays the lost removal without a second round.
	if err := h.tick(); err != nil {
		t.Fatal(err)
	}
	if h.has(a.issue, lblWorkerDone) || h.has(b.issue, lblWorkerDone) || h.phases(a.issue) != "requested" {
		t.Errorf("replay failed: a=%q", h.phases(a.issue))
	}
}
