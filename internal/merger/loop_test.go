package merger

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/closeout"
	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/effects"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

var errBoom = errors.New("boom")

func (h *harness) mergeCalls() []github.MergeInput {
	var out []github.MergeInput
	for _, c := range h.calls("MergePR") {
		out = append(out, c.Args[1].(github.MergeInput))
	}
	return out
}

func TestLoopIdentity(t *testing.T) {
	l := New(Deps{})
	if l.Name() != "merger" || !l.Claims() {
		t.Errorf("name %q claims %v", l.Name(), l.Claims())
	}
	if got := l.Interval(nil); got != 5*time.Minute {
		t.Errorf("default interval %v", got)
	}
	if got := l.Interval(&config.Resolved{Intervals: config.Intervals{Merger: config.Duration(7 * time.Minute)}}); got != 7*time.Minute {
		t.Errorf("configured interval %v", got)
	}
}

func TestMergesOneAtATime(t *testing.T) {
	h := newHarness(t)
	a, b := h.newPR(), h.newPR()
	h.settle()
	out := h.mustTick()
	if !strings.Contains(out, "merge.merged") {
		t.Fatalf("log: %s", out)
	}
	ms := h.mergeCalls()
	if len(ms) != 1 {
		t.Fatalf("merges = %d, want 1 (one per tick)", len(ms))
	}
	m := ms[0]
	if m.SHA != a.head || m.Title != "feat: fix it (#1) (#"+itoa(a.n)+")" || m.Message != "Ard-Job: "+a.jobID {
		t.Errorf("merge input %+v", m)
	}
	if !h.merged(a.n) || h.merged(b.n) {
		t.Error("wrong PR merged")
	}
	if h.issueOpen(a.issue) {
		t.Error("issue not closed by Closes #n")
	}
	if got := h.calls("DeleteBranch"); len(got) != 1 || got[0].Args[0] != a.branch {
		t.Errorf("delete branch calls: %+v", got)
	}
	evs, err := h.store.Events(context.Background(), state.EventFilter{Kind: state.EventMerge})
	if err != nil || len(evs) != 1 || evs[0].Issue != a.issue || !strings.Contains(string(evs[0].Data), a.head) {
		t.Errorf("merge events: %+v %v", evs, err)
	}
	h.mustTick()
	if len(h.mergeCalls()) != 2 || !h.merged(b.n) {
		t.Errorf("second PR not merged on the next tick: %d", len(h.mergeCalls()))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// TestNoMerge: every blocking condition leaves the PR alone.
func TestNoMerge(t *testing.T) {
	cases := []struct {
		name    string
		mut     func(*spec)
		post    func(*harness, pr)
		wantErr bool
	}{
		{name: "pr needs-human", mut: func(s *spec) { s.labels = append(s.labels, labels.NeedsHuman) }},
		{name: "pr hold", mut: func(s *spec) { s.labels = append(s.labels, labels.Hold) }},
		{name: "pr tests failed", mut: func(s *spec) { s.labels = append(s.labels, labels.TestsFailed) }},
		{name: "pr executor failed", mut: func(s *spec) { s.labels = append(s.labels, labels.ExecutorFailed) }},
		{name: "issue hold", mut: func(s *spec) { s.issueLbl = []string{labels.Hold} }},
		{name: "issue needs-human", mut: func(s *spec) { s.issueLbl = []string{labels.NeedsHuman} }},
		{name: "draft", mut: func(s *spec) { s.draft = true }},
		{name: "human author", mut: func(s *spec) { s.author = "someone" }},
		{name: "not a worker branch", mut: func(s *spec) { s.headRef = "feature/x" }},
		{name: "review changes", mut: func(s *spec) { s.labels = append(s.labels, labels.Changes(reviewer)) }},
		{name: "review running", mut: func(s *spec) { s.labels = append(s.labels, labels.Running(labels.StageReview, reviewer)) }},
		{name: "review pending", mut: func(s *spec) { s.labels = []string{labels.Request(labels.StageReview, reviewer)} }},
		{name: "no review requested", mut: func(s *spec) { s.labels = nil }},
		{name: "newest review close-out is changes", post: func(h *harness, p pr) {
			h.review(p.issue, p.n, p.head, closeout.OutcomeChanges)
		}},
		{name: "newest review close-out failed", post: func(h *harness, p pr) {
			h.review(p.issue, p.n, p.head, closeout.OutcomeFailed)
		}},
		{name: "changes label of an unconfigured role", mut: func(s *spec) { s.labels = append(s.labels, labels.Changes("gone-role")) }},
		{name: "no review claim", mut: func(s *spec) { s.noClaim = true }},
		{name: "running worker job", post: func(h *harness, p pr) {
			if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "worker", Role: "other", Issue: p.issue, Branch: p.branch}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "running review job", post: func(h *harness, p pr) {
			if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "review", Role: reviewer, Issue: p.issue, Branch: p.branch}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "stale review (code pushed after it)", post: func(h *harness, p pr) {
			h.gh.SetHeadSHA(p.n, h.commit(p.branch, "later.go", "package x\n"))
		}},
		{name: "head moved on the branch", post: func(h *harness, p pr) {
			h.commit(p.branch, "later.go", "package x\n") // API still reports the old head
		}},
		{name: "pending checks", post: func(h *harness, p pr) {
			h.gh.SetCheckRuns(p.head, github.CheckRun{ID: 1, Name: "ci", Status: "queued"})
		}},
		{name: "required check missing", post: func(h *harness, p pr) {
			h.gh.SetRequiredChecks("main", github.RequiredCheck{Context: "ci"})
		}},
		{name: "required checks unreadable", wantErr: true, post: func(h *harness, p pr) {
			h.gh.FailNext("RequiredChecks", errBoom)
			h.gh.FailNext("RequiredChecks", errBoom)
		}},
		{name: "rules forbidden by permission", wantErr: true, post: func(h *harness, p pr) {
			h.gh.FailNext("RequiredChecks", github.ErrForbidden)
			h.gh.FailNext("RequiredChecks", github.ErrForbidden)
		}},
		{name: "no closing ref", mut: func(s *spec) { s.body = "Opened by ard\nCloses #1" }},
		{name: "no worker job", mut: func(s *spec) { s.noJob = true }},
		{name: "mergeable unknown", post: func(h *harness, p pr) { h.gh.SetMergeableUnknown(p.n, true) }},
		{name: "conflict", mut: func(s *spec) { s.mergeSt = "dirty" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			var mut []func(*spec)
			if tc.mut != nil {
				mut = append(mut, tc.mut)
			}
			p := h.newPR(mut...)
			if tc.post != nil {
				tc.post(h, p)
			}
			h.tick() //nolint:errcheck // the first tick only records the head
			h.now = h.now.Add(3 * time.Minute)
			_, err := h.tick()
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if n := len(h.mergeCalls()); n != 0 || h.merged(p.n) {
				t.Fatalf("merged (%d calls)", n)
			}
			if len(h.calls("UpdateBranch")) != 0 {
				t.Error("updated the branch")
			}
		})
	}
}

func TestNoCIGracePeriod(t *testing.T) {
	h := newHarness(t)
	p := h.newPR()
	steps := []struct {
		advance time.Duration
		merged  bool
	}{
		{0, false},               // first sight of the head
		{time.Minute, false},     // a later tick, but under 2 minutes
		{90 * time.Second, true}, // 2.5 minutes since first seen
	}
	for i, s := range steps {
		h.now = h.now.Add(s.advance)
		h.mustTick()
		if h.merged(p.n) != s.merged {
			t.Fatalf("step %d: merged = %v", i, h.merged(p.n))
		}
	}
}

func TestGraceRestartsWhenHeadChanges(t *testing.T) {
	h := newHarness(t)
	p := h.newPR()
	h.settle()
	// The head changes (a fix push, reviewed again) just before the merge tick.
	head := h.commit(p.branch, "more.go", "package x\n")
	h.gh.SetHeadSHA(p.n, head)
	h.review(p.issue, p.n, head, closeout.OutcomeDone)
	h.mustTick()
	if h.merged(p.n) {
		t.Fatal("merged a head seen for less than the grace period")
	}
	h.now = h.now.Add(3 * time.Minute)
	h.mustTick()
	if !h.merged(p.n) {
		t.Fatal("not merged after the grace period")
	}
}

func TestChecks(t *testing.T) {
	ci := github.RequiredCheck{Context: "ci"}
	ok := github.CheckRun{ID: 2, Name: "ci", Status: "completed", Conclusion: "success", CompletedAt: t0}
	bad := github.CheckRun{ID: 2, Name: "ci", Status: "completed", Conclusion: "failure", CompletedAt: t0}
	cases := []struct {
		name      string
		rules     func(*harness)
		runs      []github.CheckRun
		wantMerge bool
		wantHuman bool
	}{
		{"required and green", func(h *harness) { h.gh.SetRequiredChecks("main", ci) }, []github.CheckRun{ok}, true, false},
		{"required and red", func(h *harness) { h.gh.SetRequiredChecks("main", ci) }, []github.CheckRun{bad}, false, true},
		{"required, red run but a later green one", func(h *harness) { h.gh.SetRequiredChecks("main", ci) },
			[]github.CheckRun{bad, {ID: 3, Name: "ci", Status: "completed", Conclusion: "success", CompletedAt: t0.Add(time.Minute)}}, true, false},
		{"rules unavailable, no runs", func(h *harness) { h.gh.SetRulesUnavailable("main") }, nil, true, false},
		{"rules unavailable, green run", func(h *harness) { h.gh.SetRulesUnavailable("main") }, []github.CheckRun{ok}, true, false},
		{"rules unavailable, red run", func(h *harness) { h.gh.SetRulesUnavailable("main") }, []github.CheckRun{bad}, false, true},
		{"rules unavailable, running", func(h *harness) { h.gh.SetRulesUnavailable("main") }, []github.CheckRun{{ID: 1, Name: "ci", Status: "in_progress"}}, false, false},
		{"rules without required checks, red run", func(h *harness) { h.gh.SetRequiredChecks("main") }, []github.CheckRun{bad}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			tc.rules(h)
			p := h.newPR()
			h.gh.SetCheckRuns(p.head, tc.runs...)
			h.settle()
			h.mustTick()
			if h.merged(p.n) != tc.wantMerge {
				t.Fatalf("merged = %v", h.merged(p.n))
			}
			if h.has(p.n, labels.NeedsHuman) != tc.wantHuman {
				t.Fatalf("needs-human = %v", h.has(p.n, labels.NeedsHuman))
			}
			if tc.wantHuman {
				cs := h.comments(p.n)
				if len(cs) != 1 || !strings.Contains(cs[0].Body, "`ci`") || !strings.Contains(cs[0].Body, "needs-human reason=checks_failed") {
					t.Errorf("comments: %+v", cs)
				}
				h.mustTick()
				if len(h.comments(p.n)) != 1 {
					t.Error("second comment posted")
				}
			}
		})
	}
}

func TestChecksFromCommitStatuses(t *testing.T) {
	jenkins := github.RequiredCheck{Context: "continuous-integration/jenkins/pr-merge"}
	st := func(state string) github.CommitStatus {
		return github.CommitStatus{Context: jenkins.Context, State: state, UpdatedAt: t0}
	}
	cases := []struct {
		name      string
		statuses  []github.CommitStatus
		readErr   bool
		wantMerge bool
		wantHuman bool
	}{
		{"jenkins success merges", []github.CommitStatus{st("success")}, false, true, false},
		{"jenkins pending waits", []github.CommitStatus{st("pending")}, false, false, false},
		{"jenkins failure needs human", []github.CommitStatus{st("failure")}, false, false, true},
		{"statuses read error fails closed", []github.CommitStatus{st("success")}, true, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.gh.SetRequiredChecks("main", jenkins)
			p := h.newPR()
			h.gh.SetCommitStatuses(p.head, tc.statuses...)
			if tc.readErr {
				h.gh.FailNext("ListCommitStatuses", errBoom)
			}
			if _, err := h.tick(); (err != nil) != tc.readErr {
				t.Fatalf("tick error = %v, want error %v", err, tc.readErr)
			}
			h.now = h.now.Add(3 * time.Minute)
			if tc.readErr {
				if h.merged(p.n) {
					t.Fatal("merged on a statuses read error")
				}
				h.mustTick() // the next tick recovers
				if !h.merged(p.n) {
					t.Fatal("not merged once statuses are readable")
				}
				return
			}
			h.mustTick()
			if h.merged(p.n) != tc.wantMerge {
				t.Fatalf("merged = %v", h.merged(p.n))
			}
			if h.has(p.n, labels.NeedsHuman) != tc.wantHuman {
				t.Fatalf("needs-human = %v", h.has(p.n, labels.NeedsHuman))
			}
		})
	}
}

func TestPinnedStatusApp(t *testing.T) {
	const ctxName = "continuous-integration/jenkins/pr-merge"
	pinned := github.RequiredCheck{Context: ctxName, IntegrationID: 5884800}
	mk := func(login string) []github.CommitStatus {
		return []github.CommitStatus{{Context: ctxName, State: "success", UpdatedAt: t0, Creator: login}}
	}
	cases := []struct {
		name      string
		statuses  []github.CommitStatus
		lookupErr bool
		wantMerge bool // after the lookup error (if any) and a retry tick
	}{
		{"right app", mk("jenkins[bot]"), false, true},
		{"other app", mk("other[bot]"), false, false},
		{"user", mk("someone"), false, false},
		{"lookup error fails closed, then recovers", mk("jenkins[bot]"), true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			h.gh.SetRequiredChecks("main", pinned)
			h.gh.SetAppID("jenkins", 5884800)
			h.gh.SetAppID("other", 1)
			p := h.newPR()
			h.gh.SetCommitStatuses(p.head, tc.statuses...)
			if tc.lookupErr {
				h.gh.FailNext("AppID", errBoom)
				if _, err := h.tick(); err == nil {
					t.Fatal("no tick error")
				}
				if h.merged(p.n) {
					t.Fatal("merged on a lookup error")
				}
			}
			h.now = h.now.Add(3 * time.Minute)
			h.mustTick()
			if h.merged(p.n) != tc.wantMerge {
				t.Fatalf("merged = %v", h.merged(p.n))
			}
		})
	}
}

func TestUnevaluatedRulesWarn(t *testing.T) {
	h := newHarness(t)
	h.gh.SetUnevaluatedRules("main", "workflows")
	h.newPR()
	out := h.mustTick()
	if !strings.Contains(out, "merge.rules_not_evaluated") || !strings.Contains(out, "workflows") {
		t.Errorf("log: %s", out)
	}
}

func TestBehindUpdatesOnePerTick(t *testing.T) {
	h := newHarness(t)
	a, b := h.newPR(), h.newPR()
	h.commit("main", "main_only.go", "package m\n")
	out := h.mustTick()
	ups := h.calls("UpdateBranch")
	if len(ups) != 1 || ups[0].Args[0] != a.n || ups[0].Args[1] != a.head {
		t.Fatalf("UpdateBranch calls: %+v", ups)
	}
	if !strings.Contains(out, "merge.update_branch") || !strings.Contains(out, "merge.waiting_update") {
		t.Errorf("log: %s", out)
	}
	if len(h.mergeCalls()) != 0 {
		t.Fatal("merged while behind")
	}
	// GitHub updated a's branch for real; reviews still cover the merged head.
	h.updateBranch(a.n, a.branch)
	h.mustTick()
	h.now = h.now.Add(3 * time.Minute)
	h.mustTick()
	if !h.merged(a.n) || h.merged(b.n) {
		t.Fatalf("a merged = %v, b merged = %v", h.merged(a.n), h.merged(b.n))
	}
}

func TestOtherIssuesArdCommitsDoNotMakeBehind(t *testing.T) {
	h := newHarness(t)
	p := h.newPR()
	h.commit("main", ".ard/99/plan.md", "plan\n")
	h.settle()
	h.mustTick()
	if len(h.calls("UpdateBranch")) != 0 || !h.merged(p.n) {
		t.Fatalf("updates = %d merged = %v", len(h.calls("UpdateBranch")), h.merged(p.n))
	}
}

func TestUpdateBranchChangesInvalidateNothingButCodeAfterReviewDoes(t *testing.T) {
	h := newHarness(t)
	p := h.newPR()
	h.commit("main", "main_only.go", "package m\n")
	h.updateBranch(p.n, p.branch)
	// A code push on top of the update after the review needs a new review.
	head := h.commit(p.branch, "extra.go", "package x\n")
	h.gh.SetHeadSHA(p.n, head)
	h.settle()
	out := h.mustTick()
	if h.merged(p.n) || !strings.Contains(out, "merge.reviews_stale") {
		t.Fatalf("merged = %v\n%s", h.merged(p.n), out)
	}
}

// flipEffects makes UpdateBranch report a conflict after GitHub computed one.
type flipEffects struct {
	effects.Effects
	h *harness
}

func (f flipEffects) UpdateBranch(_ context.Context, n int, _ string) error {
	f.h.gh.SetMergeableState(n, "dirty")
	return github.ErrConflict
}

func TestConflict(t *testing.T) {
	t.Run("dirty", func(t *testing.T) {
		h := newHarness(t)
		p := h.newPR(func(s *spec) { s.mergeSt = "dirty" })
		h.settle()
		h.mustTick()
		h.mustTick()
		if !h.has(p.n, labels.NeedsHuman) || h.merged(p.n) {
			t.Fatal("conflict not sent to a human")
		}
		cs := h.comments(p.n)
		if len(cs) != 1 || !strings.Contains(cs[0].Body, "reason=conflict") || !strings.Contains(cs[0].Body, "#41") {
			t.Errorf("comments: %+v", cs)
		}
	})
	t.Run("update refused with the head unchanged", func(t *testing.T) {
		h := newHarness(t)
		p := h.newPR()
		h.commit("main", "main_only.go", "package m\n")
		h.loop.d.Effects = flipEffects{h.fx, h}
		h.settle()
		h.mustTick()
		h.mustTick()
		if !h.has(p.n, labels.NeedsHuman) || len(h.comments(p.n)) != 1 {
			t.Fatalf("labels %v comments %d", h.labelsOf(p.n), len(h.comments(p.n)))
		}
	})
	t.Run("update refused because the head moved", func(t *testing.T) {
		h := newHarness(t)
		p := h.newPR()
		h.commit("main", "main_only.go", "package m\n")
		h.settle()
		h.gh.FailNext("UpdateBranch", github.ErrConflict)
		h.mustTick()
		if h.has(p.n, labels.NeedsHuman) || len(h.comments(p.n)) != 0 {
			t.Fatal("a moved head was treated as a conflict")
		}
	})
}

func TestNotMergeableRetriesWithoutLabel(t *testing.T) {
	for name, fail := range map[string]error{"405": github.ErrNotMergeable, "head moved": github.ErrConflict} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			p := h.newPR()
			h.settle()
			h.gh.FailNext("MergePR", fail)
			h.mustTick()
			if h.merged(p.n) || len(h.labelsOf(p.n)) != 2 || len(h.comments(p.n)) != 0 || len(h.calls("DeleteBranch")) != 0 {
				t.Fatalf("labels %v", h.labelsOf(p.n))
			}
			h.mustTick()
			if !h.merged(p.n) {
				t.Fatal("not retried next tick")
			}
		})
	}
	t.Run("mergeable unknown then known", func(t *testing.T) {
		h := newHarness(t)
		p := h.newPR()
		h.gh.SetMergeableUnknown(p.n, true)
		h.settle()
		out := h.mustTick()
		if h.merged(p.n) || h.has(p.n, labels.NeedsHuman) || !strings.Contains(out, "merge.mergeable_unknown") {
			t.Fatalf("unknown mergeable mishandled\n%s", out)
		}
		h.gh.SetMergeableUnknown(p.n, false)
		h.mustTick()
		if !h.merged(p.n) {
			t.Fatal("not merged once known")
		}
	})
	t.Run("other merge error", func(t *testing.T) {
		h := newHarness(t)
		p := h.newPR()
		h.settle()
		h.gh.FailNext("MergePR", errBoom)
		if _, err := h.tick(); !errors.Is(err, errBoom) {
			t.Fatalf("err = %v", err)
		}
		if h.merged(p.n) {
			t.Fatal("merged")
		}
	})
}

func TestMissingClosingRefAndJobGoToHuman(t *testing.T) {
	for name, tc := range map[string]struct {
		mut    func(*spec)
		reason string
	}{
		"closing ref": {func(s *spec) { s.body = "Opened by ard\n" }, "no_closing_ref"},
		"worker job":  {func(s *spec) { s.noJob = true }, "no_worker_job"},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			p := h.newPR(tc.mut)
			h.settle()
			h.mustTick()
			cs := h.comments(p.n)
			if !h.has(p.n, labels.NeedsHuman) || len(cs) != 1 || !strings.Contains(cs[0].Body, "reason="+tc.reason) {
				t.Fatalf("labels %v comments %+v", h.labelsOf(p.n), cs)
			}
		})
	}
}

func TestProtectedPaths(t *testing.T) {
	cases := []struct {
		name   string
		files  map[string]string
		setup  func(*harness)
		rename [2]string
	}{
		{name: "github dir", files: map[string]string{".github/workflows/x.yml": "x"}},
		{name: "claude dir", files: map[string]string{".claude/a": "x"}},
		{name: "config", files: map[string]string{".ard/config.yaml": "x"}},
		{name: "control character path", files: map[string]string{"weird\x01name.txt": "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			p := h.newPR(func(s *spec) { s.files = tc.files })
			h.settle()
			h.mustTick()
			h.mustTick()
			if h.merged(p.n) || !h.has(p.n, labels.NeedsApproval) {
				t.Fatalf("merged %v labels %v", h.merged(p.n), h.labelsOf(p.n))
			}
			if cs := h.comments(p.n); len(cs) != 1 || !strings.Contains(cs[0].Body, "approval head="+p.head) {
				t.Fatalf("comments: %+v", cs)
			}
			h.gh.SetPermission("alice", github.Permission{Level: "write", Role: "write"})
			h.gh.SeedReview(p.n, github.Review{Author: "alice", State: "APPROVED", CommitSHA: p.head})
			h.mustTick()
			if !h.merged(p.n) {
				t.Fatal("not merged after a write-access approval")
			}
		})
	}
}

func TestProtectedRename(t *testing.T) {
	h := newHarness(t)
	h.commit("main", "docs/a", "x\n")
	p := h.newPR(func(s *spec) { s.files = map[string]string{"unused.go": "package x\n"} })
	h.git("checkout", "-q", p.branch)
	if err := os.MkdirAll(filepath.Join(h.clone, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.git("mv", "docs/a", ".claude/a")
	h.git("commit", "-q", "-m", "rename")
	head := h.git("rev-parse", "HEAD")
	h.git("push", "-q", "-f", h.bare, p.branch)
	h.git("checkout", "-q", "main")
	h.gh.SetHeadSHA(p.n, head)
	h.review(p.issue, p.n, head, closeout.OutcomeDone)
	h.settle()
	h.mustTick()
	if h.merged(p.n) || !h.has(p.n, labels.NeedsApproval) {
		t.Fatalf("rename into .claude/ merged or unlabelled: %v", h.labelsOf(p.n))
	}
	// Renaming a protected file out of its directory is protected by the old name too.
	h.gh.SetPermission("alice", github.Permission{Level: "write", Role: "write"})
	h.gh.SeedReview(p.n, github.Review{Author: "alice", State: "APPROVED", CommitSHA: head})
	h.mustTick()
	if !h.merged(p.n) {
		t.Fatal("not merged after approval")
	}
}

func TestProtectedApprovalRules(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *harness, p pr)
	}{
		{"App approval ignored", func(h *harness, p pr) {
			h.gh.SetPermission(self, github.Permission{Level: "admin", Role: "admin"})
			h.gh.SeedReview(p.n, github.Review{Author: self, State: "APPROVED", CommitSHA: p.head})
		}},
		{"triage user", func(h *harness, p pr) {
			h.gh.SetPermission("bob", github.Permission{Level: "read", Role: "triage"})
			h.gh.SeedReview(p.n, github.Review{Author: "bob", State: "APPROVED", CommitSHA: p.head})
		}},
		{"read user", func(h *harness, p pr) {
			h.gh.SeedReview(p.n, github.Review{Author: "carol", State: "APPROVED", CommitSHA: p.head})
		}},
		{"approval then changes requested", func(h *harness, p pr) {
			h.gh.SetPermission("alice", github.Permission{Level: "write", Role: "write"})
			h.gh.SeedReview(p.n, github.Review{Author: "alice", State: "APPROVED", CommitSHA: p.head, SubmittedAt: t0})
			h.gh.SeedReview(p.n, github.Review{Author: "alice", State: "CHANGES_REQUESTED", CommitSHA: p.head, SubmittedAt: t0.Add(time.Minute)})
		}},
		{"dismissed", func(h *harness, p pr) {
			h.gh.SetPermission("alice", github.Permission{Level: "write", Role: "write"})
			h.gh.SeedReview(p.n, github.Review{Author: "alice", State: "APPROVED", CommitSHA: p.head, SubmittedAt: t0})
			h.gh.SeedReview(p.n, github.Review{Author: "alice", State: "DISMISSED", CommitSHA: p.head, SubmittedAt: t0.Add(time.Minute)})
		}},
		{"approval before a code push", func(h *harness, p pr) {
			h.gh.SetPermission("alice", github.Permission{Level: "write", Role: "write"})
			h.gh.SeedReview(p.n, github.Review{Author: "alice", State: "APPROVED", CommitSHA: p.head})
			head := h.commit(p.branch, "later.go", "package x\n")
			h.gh.SetHeadSHA(p.n, head)
			h.review(p.issue, p.n, head, closeout.OutcomeDone) // the reviewers re-reviewed the new head
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			p := h.newPR(func(s *spec) { s.files = map[string]string{".github/x": "x"} })
			tc.setup(h, p)
			h.settle()
			h.mustTick()
			if h.merged(p.n) || !h.has(p.n, labels.NeedsApproval) {
				t.Fatalf("merged %v labels %v", h.merged(p.n), h.labelsOf(p.n))
			}
		})
	}
	t.Run("approval at an old head with only main's changes since", func(t *testing.T) {
		h := newHarness(t)
		p := h.newPR(func(s *spec) { s.files = map[string]string{".github/x": "x"} })
		h.gh.SetPermission("alice", github.Permission{Level: "write", Role: "write"})
		h.gh.SeedReview(p.n, github.Review{Author: "alice", State: "APPROVED", CommitSHA: p.head})
		h.commit("main", "main_only.go", "package m\n")
		h.updateBranch(p.n, p.branch)
		h.settle()
		h.mustTick()
		h.now = h.now.Add(3 * time.Minute)
		h.mustTick()
		if !h.merged(p.n) {
			t.Fatalf("not merged; labels %v", h.labelsOf(p.n))
		}
	})
}

func TestPausedLoopIsSkippedByTheController(t *testing.T) {
	if !New(Deps{}).Claims() {
		t.Fatal("Claims() must be true so the controller skips the loop while paused")
	}
}

func TestDryRun(t *testing.T) {
	t.Run("merge", func(t *testing.T) {
		h := newHarness(t, dryRun)
		a, b := h.newPR(), h.newPR()
		h.settle()
		out := h.mustTick()
		if strings.Count(out, "merge.would_merge") != 2 {
			t.Fatalf("both PRs should be would_merge in one dry-run tick:\n%s", out)
		}
		if w := h.gh.Writes(); len(w) != 0 {
			t.Fatalf("dry-run wrote to GitHub: %+v", w)
		}
		led := readLedger(t, h.ledger)
		if strings.Count(led, "MergePR") != 2 || strings.Count(led, "DeleteBranch") != 2 {
			t.Errorf("ledger: %s", led)
		}
		if h.merged(a.n) || h.merged(b.n) {
			t.Error("merged for real")
		}
		evs, _ := h.store.Events(context.Background(), state.EventFilter{Kind: state.EventMerge})
		if len(evs) != 0 {
			t.Error("recorded a merge event in dry-run")
		}
	})
	t.Run("update", func(t *testing.T) {
		h := newHarness(t, dryRun)
		h.newPR()
		h.commit("main", "main_only.go", "package m\n")
		out := h.mustTick()
		if !strings.Contains(out, "merge.would_update") || strings.Count(readLedger(t, h.ledger), "UpdateBranch") != 1 || len(h.gh.Writes()) != 0 {
			t.Fatalf("log: %s\nledger: %s", out, readLedger(t, h.ledger))
		}
	})
	t.Run("labels and comments", func(t *testing.T) {
		h := newHarness(t, dryRun)
		h.newPR(func(s *spec) { s.mergeSt = "dirty" })
		h.settle()
		h.mustTick()
		led := readLedger(t, h.ledger)
		if !strings.Contains(led, "AddLabels") || !strings.Contains(led, "CreateComment") || len(h.gh.Writes()) != 0 {
			t.Fatalf("ledger: %s", led)
		}
	})
}

func readLedger(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}
