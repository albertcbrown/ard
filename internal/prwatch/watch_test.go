package prwatch

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
)

func green(name string) github.CheckRun {
	return github.CheckRun{ID: 1, Name: name, Status: "completed", Conclusion: "success", CompletedAt: t0}
}

func red(name string) github.CheckRun {
	return github.CheckRun{ID: 2, Name: name, Status: "completed", Conclusion: "failure", CompletedAt: t0}
}

func queued(name string) github.CheckRun {
	return github.CheckRun{ID: 3, Name: name, Status: "queued"}
}

func TestClosedUnmerged(t *testing.T) {
	tests := []struct {
		name string
		// arrange runs after the first tick saw the PR open, before it is closed.
		arrange func(h *harness, pr, issue int)
		want    bool
	}{
		{name: "closed_unmerged", want: true},
		{name: "closed_issue_closed", arrange: func(h *harness, _, issue int) { h.close(issue) }},
		{name: "closed_but_merged", arrange: func(h *harness, pr, issue int) {
			data, _ := json.Marshal(map[string]any{"pr": pr, "head": "h1"})
			if err := h.store.AppendEvent(context.Background(), state.Event{Kind: state.EventMerge, Issue: issue, Data: data}); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "another_pr_open_for_the_issue", arrange: func(h *harness, _, issue int) {
			h.gh.SeedPR(github.PullRequest{Author: self, State: github.StateOpen, HeadRef: "worker/eng-" + itoa(issue) + "-again", HeadSHA: "h2", BaseRef: "main"})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, false)
			pr, issue := h.pr("h1")
			h.mustTick()
			if tc.arrange != nil {
				tc.arrange(h, pr, issue)
			}
			h.close(pr)
			h.mustTick()
			h.mustTick()
			h.restart()
			h.mustTick()
			got := h.comments(issue)
			if !tc.want {
				if len(got) != 0 || has(h.issueLabels(issue), labels.NeedsHuman) {
					t.Fatalf("acted: comments=%v labels=%v", got, h.issueLabels(issue))
				}
				return
			}
			if len(got) != 1 || !has(h.issueLabels(issue), labels.NeedsHuman) {
				t.Fatalf("want one comment and the label: comments=%v labels=%v", got, h.issueLabels(issue))
			}
			if !strings.Contains(got[0], "`"+labels.Done(labels.StageWorker, "eng")+"`") || strings.Contains(got[0], "@") {
				t.Errorf("comment = %q", got[0])
			}
			if n := len(h.events(state.EventPRWatch)); n != 1 {
				t.Errorf("pr_watch events = %d, want 1", n)
			}
		})
	}
}

func TestClosedBeforeFirstSightIsHistory(t *testing.T) {
	h := newHarness(t, false)
	pr, issue := h.pr("h1")
	h.close(pr)
	h.mustTick()
	if len(h.comments(issue)) != 0 || has(h.issueLabels(issue), labels.NeedsHuman) {
		t.Fatal("acted on a PR never seen open")
	}
}

func TestPendingTimeout(t *testing.T) {
	setup := func(t *testing.T) (*harness, int) {
		h := newHarness(t, false)
		h.gh.SetRequiredChecks("main", github.RequiredCheck{Context: "ci"})
		pr, _ := h.pr("h1")
		h.gh.SetCheckRuns("h1", queued("ci"))
		h.mustTick()
		return h, pr
	}
	t.Run("pending_under_timeout", func(t *testing.T) {
		h, _ := setup(t)
		h.now = t0.Add(2 * time.Hour)
		h.mustTick()
		if got := h.comments(h.status); len(got) != 0 {
			t.Fatalf("notice under the timeout: %v", got)
		}
	})
	t.Run("pending_over_timeout", func(t *testing.T) {
		h, pr := setup(t)
		h.now = t0.Add(2*time.Hour + time.Minute)
		h.mustTick()
		h.mustTick()
		h.restart()
		h.now = h.now.Add(time.Hour)
		h.mustTick()
		got := h.comments(h.status)
		if len(got) != 1 {
			t.Fatalf("notices = %v, want 1", got)
		}
		if !strings.Contains(got[0], "`ci`") || !strings.Contains(got[0], "pr="+itoa(pr)) || len(h.comments(pr)) != 0 {
			t.Errorf("notice = %q", got[0])
		}
	})
	t.Run("lost_event_does_not_repost", func(t *testing.T) {
		h, pr := setup(t)
		h.now = t0.Add(3 * time.Hour)
		h.mustTick()
		// A crash between the comment and the event write: a store with neither,
		// but the old head sighting. The marker comment alone stops a repost.
		fresh, err := state.Open(context.Background(), t.TempDir(), state.Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { fresh.Close() })
		if err := fresh.PutPRWatch(context.Background(), state.PRWatch{PR: pr, Issue: 2, Head: "h1", HeadSeenAt: t0}); err != nil {
			t.Fatal(err)
		}
		h.store = fresh
		h.restart()
		h.mustTick()
		h.mustTick()
		if got := h.comments(h.status); len(got) != 1 {
			t.Fatalf("notices = %v, want 1", got)
		}
		if n := len(h.events(state.EventPRWatch)); n != 1 {
			t.Errorf("event not backfilled: %d", n)
		}
	})
	t.Run("new_head_resets_clock", func(t *testing.T) {
		h, pr := setup(t)
		h.now = t0.Add(3 * time.Hour)
		h.gh.SetHeadSHA(pr, "h2")
		h.gh.SetCheckRuns("h2", queued("ci"))
		h.mustTick() // new head seen now: clock restarts
		if got := h.comments(h.status); len(got) != 0 {
			t.Fatalf("notice right after a new head: %v", got)
		}
		h.now = h.now.Add(2*time.Hour + time.Minute)
		h.mustTick()
		got := h.comments(h.status)
		if len(got) != 1 || !strings.Contains(got[0], "head=h2") {
			t.Fatalf("notices = %v, want one for h2", got)
		}
	})
	t.Run("no_status_issue_waits", func(t *testing.T) {
		h, _ := setup(t)
		status := h.status
		h.status = 0
		h.now = t0.Add(3 * time.Hour)
		h.mustTick()
		if got := h.comments(status); len(got) != 0 {
			t.Fatalf("notice without a status issue: %v", got)
		}
		h.status = status
		h.mustTick()
		if got := h.comments(status); len(got) != 1 {
			t.Fatalf("notices = %v, want 1 once known", got)
		}
	})
}

// #256: checks reported only as green commit statuses are never pending.
func TestStatusesOnlyGreenNeverPending(t *testing.T) {
	h := newHarness(t, false)
	h.gh.SetRequiredChecks("main", github.RequiredCheck{Context: "jenkins"})
	h.pr("h1")
	h.gh.SetCommitStatuses("h1", github.CommitStatus{Context: "jenkins", State: "success", UpdatedAt: t0})
	h.mustTick()
	h.now = t0.Add(5 * time.Hour)
	h.mustTick()
	if got := h.comments(h.status); len(got) != 0 {
		t.Fatalf("notice for green statuses: %v", got)
	}
}

func TestHumanMerge(t *testing.T) {
	tests := []struct {
		name   string
		by     string
		events int
		actor  string
	}{
		{name: "human_merge", by: "alice", events: 1, actor: "alice"},
		{name: "ard_merge_not_recorded_twice", by: self, events: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, false)
			pr, _ := h.pr("h1")
			h.mustTick()
			if _, err := h.gh.As(tc.by).MergePR(context.Background(), pr, github.MergeInput{SHA: "h1"}); err != nil {
				t.Fatal(err)
			}
			h.mustTick()
			h.restart()
			h.mustTick()
			evs := h.events(state.EventMerge)
			if len(evs) != tc.events {
				t.Fatalf("merge events = %d, want %d", len(evs), tc.events)
			}
			if tc.events == 1 {
				var d struct {
					PR    int    `json:"pr"`
					Head  string `json:"head"`
					Actor string `json:"actor"`
				}
				if err := json.Unmarshal(evs[0].Data, &d); err != nil || d.Actor != tc.actor || d.PR != pr || d.Head != "h1" {
					t.Errorf("data = %s, %v", evs[0].Data, err)
				}
			}
		})
	}
}

func TestArdMergeWithEventIsNotRecorded(t *testing.T) {
	h := newHarness(t, false)
	pr, issue := h.pr("h1")
	h.mustTick()
	if _, err := h.gh.As("alice").MergePR(context.Background(), pr, github.MergeInput{SHA: "h1"}); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"pr": pr, "head": "h1", "merge_sha": "m"})
	if err := h.store.AppendEvent(context.Background(), state.Event{Kind: state.EventMerge, Issue: issue, Data: data}); err != nil {
		t.Fatal(err)
	}
	h.mustTick()
	if n := len(h.events(state.EventMerge)); n != 1 {
		t.Fatalf("merge events = %d, want only the merger's", n)
	}
}

func TestDraftRed(t *testing.T) {
	tests := []struct {
		name  string
		opts  []prOpt
		want  int
		extra bool // a second head, also red
	}{
		{name: "draft_red", opts: []prOpt{draft}, want: 1},
		{name: "draft_red_new_head_second_comment", opts: []prOpt{draft}, want: 2, extra: true},
		{name: "ready_red", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, false)
			h.gh.SetRequiredChecks("main", github.RequiredCheck{Context: "ci"}, github.RequiredCheck{Context: "lint"})
			pr, _ := h.pr("h1", tc.opts...)
			h.gh.SetCheckRuns("h1", red("ci"), green("lint"))
			h.mustTick()
			h.mustTick()
			h.restart()
			h.mustTick()
			if tc.extra {
				h.gh.SetHeadSHA(pr, "h2")
				h.gh.SetCheckRuns("h2", red("lint"), green("ci"))
				h.mustTick()
				h.mustTick()
			}
			got := h.comments(pr)
			if len(got) != tc.want {
				t.Fatalf("comments = %v, want %d", got, tc.want)
			}
			if tc.want > 0 && (!strings.Contains(got[0], "`ci`") || strings.Contains(got[0], "`lint`")) {
				t.Errorf("comment = %q", got[0])
			}
			if tc.extra && !strings.Contains(got[1], "`lint`") {
				t.Errorf("second comment = %q", got[1])
			}
		})
	}
}

func TestCheckCalls(t *testing.T) {
	h := newHarness(t, false)
	h.gh.SetRequiredChecks("main", github.RequiredCheck{Context: "ci"})
	const n = 4
	for i := 0; i < n; i++ {
		h.pr("sha"+itoa(i), draft)
		h.gh.SetCheckRuns("sha"+itoa(i), green("ci"))
	}
	h.rd.reset()
	h.mustTick()
	c := h.rd
	if got := c.get("ListCheckRuns") + c.get("ListCommitStatuses"); got != 2*n || c.get("ListPRs") != 1 || c.get("RequiredChecks") != 1 {
		t.Fatalf("first tick calls = %v", c.calls)
	}
	c.reset()
	h.mustTick()
	h.restart()
	h.mustTick()
	if got := c.get("ListCheckRuns") + c.get("ListCommitStatuses"); got != 0 {
		t.Fatalf("settled heads cost %d check calls, want 0 (%v)", got, c.calls)
	}
	if c.get("RequiredChecks") != 0 {
		t.Errorf("RequiredChecks read with nothing to judge: %v", c.calls)
	}
}

func TestRunningJobSkips(t *testing.T) {
	h := newHarness(t, false)
	pr, issue := h.pr("h1")
	h.mustTick()
	if _, err := h.store.CreateJob(context.Background(), state.NewJob{Stage: "worker", Role: "eng", Issue: issue}); err != nil {
		t.Fatal(err)
	}
	h.close(pr)
	h.mustTick()
	if len(h.comments(issue)) != 0 {
		t.Fatal("acted while a worker job runs")
	}
}

func TestIgnoredPRs(t *testing.T) {
	for name, o := range map[string]prOpt{"foreign_author": foreign, "non_worker_branch": badBranch} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, false)
			pr, issue := h.pr("h1", o)
			h.mustTick()
			h.close(pr)
			h.mustTick()
			if len(h.comments(issue)) != 0 {
				t.Fatal("acted on a PR that is not ard's")
			}
		})
	}
}

func TestDryRunWritesLedgerOnly(t *testing.T) {
	h := newHarness(t, true)
	h.gh.SetRequiredChecks("main", github.RequiredCheck{Context: "ci"})
	pr, issue := h.pr("h1")
	h.mustTick()
	h.close(pr)
	h.mustTick()
	h.mustTick()
	if len(h.gh.Writes()) != 1 { // only the harness's own close
		t.Fatalf("dry run wrote to GitHub: %v", h.gh.Writes())
	}
	for _, k := range []string{state.EventPRWatch, state.EventMerge} {
		if n := len(h.events(k)); n != 0 {
			t.Errorf("%s events = %d in a dry run", k, n)
		}
	}
	if _, ok, _ := h.store.GetPRWatch(context.Background(), pr); ok {
		t.Error("dry run wrote a row")
	}
	b, err := os.ReadFile(h.ledger)
	if err != nil {
		t.Fatal(err)
	}
	// One label and one comment, once, though the loop ticked twice after the close.
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Errorf("ledger has %d lines, want 2:\n%s", n, b)
	}
	_ = issue
}

func TestErrorsAreIsolated(t *testing.T) {
	h := newHarness(t, false)
	a, ia := h.pr("h1")
	b, ib := h.pr("h2")
	h.mustTick()
	h.close(a)
	h.close(b)
	h.gh.FailNext("GetIssue", errors.New("boom"))
	if err := h.tick(); err == nil {
		t.Fatal("tick hid the GitHub error")
	}
	if len(h.comments(ia))+len(h.comments(ib)) != 1 {
		t.Fatal("the failing PR blocked the other")
	}
	h.mustTick() // the failed one retries, the done one does not repeat
	if len(h.comments(ia)) != 1 || len(h.comments(ib)) != 1 {
		t.Fatalf("after retry: %v %v", h.comments(ia), h.comments(ib))
	}
}

func TestEffectFailureLeavesNoEvent(t *testing.T) {
	h := newHarness(t, false)
	pr, issue := h.pr("h1")
	h.mustTick()
	h.close(pr)
	h.gh.FailNext("CreateComment", errors.New("boom"))
	if err := h.tick(); err == nil {
		t.Fatal("tick hid the effect failure")
	}
	if n := len(h.events(state.EventPRWatch)); n != 0 {
		t.Fatalf("event after a failed effect: %d", n)
	}
	h.mustTick()
	if len(h.comments(issue)) != 1 || len(h.events(state.EventPRWatch)) != 1 {
		t.Fatal("the retry did not post exactly once")
	}
}

func TestSanitize(t *testing.T) {
	long := strings.Repeat("x", 500)
	tests := map[string]string{
		"ci/build":            "ci/build",
		"a`b":                 "ab",
		"@org/team ping":      "org/team ping",
		"line1\nline2\r\x00x": "line1 line2  x",
		"":                    "?",
		"``":                  "?",
		"<!-- x -->":          "!-- x --",
	}
	for in, want := range tests {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeName(long); len([]rune(got)) != maxNameRunes+1 {
		t.Errorf("long name not capped: %d runes", len([]rune(got)))
	}
	if got := codeList([]string{"a`b", "c"}); got != "`ab`, `c`" {
		t.Errorf("codeList = %q", got)
	}
}

func TestLoopBasics(t *testing.T) {
	l := New(Deps{})
	if !l.Claims() || l.Name() != "prwatch" {
		t.Fatal("prwatch must be skipped while paused")
	}
	if got := l.Interval(nil); got != 5*time.Minute {
		t.Errorf("default interval = %v", got)
	}
	if got := l.Interval(&config.Resolved{Intervals: config.Intervals{PRWatch: config.Duration(7 * time.Minute)}}); got != 7*time.Minute {
		t.Errorf("configured interval = %v", got)
	}
}
