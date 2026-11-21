// Package clienttest is a conformance suite for github.Client. It runs
// against the in-memory fake in unit tests and against the real client under
// the integration build tag, so the fake cannot drift from GitHub's behaviour.
// It imports testing and must be used only from tests.
package clienttest

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/albertcbrown/ard/internal/github"
)

// Option tunes Run.
type Option func(*config)

type config struct {
	newPR func(t *testing.T, c github.Client) (n int, headSHA string)
}

// WithPR supplies a way to obtain an open, mergeable PR and its head SHA. The
// merge-guard case is skipped without it (a real repo needs real commits).
func WithPR(f func(t *testing.T, c github.Client) (int, string)) Option {
	return func(cfg *config) { cfg.newPR = f }
}

// Run executes the suite. newClient must return a client on an isolated
// repository state for each call.
func Run(t *testing.T, newClient func(t *testing.T) github.Client, opts ...Option) {
	var cfg config
	for _, o := range opts {
		o(&cfg)
	}
	ctx := context.Background()
	uniq := fmt.Sprintf("ct%d", time.Now().UnixNano()%1e9)

	t.Run("CreateThenGet", func(t *testing.T) {
		c := newClient(t)
		in, err := c.CreateIssue(ctx, github.NewIssue{Title: "t1", Body: "b1", Labels: []string{uniq + "-a"}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.GetIssue(ctx, in.Number)
		if err != nil {
			t.Fatal(err)
		}
		if got.Title != "t1" || got.Body != "b1" || got.State != github.StateOpen || got.IsPR {
			t.Errorf("got %+v", got)
		}
		if len(got.Labels) != 1 || got.Labels[0] != uniq+"-a" {
			t.Errorf("labels = %v", got.Labels)
		}
		if got.NodeID == "" || got.URL == "" {
			t.Errorf("missing node id or url: %+v", got)
		}
		_, err = c.GetIssue(ctx, 999999)
		if !errors.Is(err, github.ErrNotFound) {
			t.Errorf("missing issue err = %v", err)
		}
	})

	t.Run("LabelAddIsIdempotentAndAutoCreates", func(t *testing.T) {
		c := newClient(t)
		is := mustIssue(t, c, "labels")
		l := uniq + "-new"
		for i := 0; i < 2; i++ {
			if err := c.AddLabels(ctx, is.Number, l); err != nil {
				t.Fatal(err)
			}
		}
		got, _ := c.GetIssue(ctx, is.Number)
		if len(got.Labels) != 1 {
			t.Errorf("labels = %v, want exactly one", got.Labels)
		}
		if n := countEvents(t, c, is.Number, "labeled", l); n != 1 {
			t.Errorf("labeled events = %d, want 1", n)
		}
		labels, err := c.ListLabels(ctx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, x := range labels {
			found = found || x.Name == l
		}
		if !found {
			t.Errorf("label %q not auto-created", l)
		}
		if err := c.RemoveLabel(ctx, is.Number, l); err != nil {
			t.Fatal(err)
		}
		if err := c.RemoveLabel(ctx, is.Number, l); err != nil {
			t.Errorf("removing an absent label: %v", err)
		}
		if n := countEvents(t, c, is.Number, "unlabeled", l); n != 1 {
			t.Errorf("unlabeled events = %d, want 1", n)
		}
	})

	t.Run("CreateLabelConflict", func(t *testing.T) {
		c := newClient(t)
		l := github.Label{Name: uniq + "-lbl", Color: "ff0000", Description: "d"}
		if err := c.CreateLabel(ctx, l); err != nil {
			t.Fatal(err)
		}
		if err := c.CreateLabel(ctx, l); !errors.Is(err, github.ErrConflict) {
			t.Errorf("second create err = %v, want ErrConflict", err)
		}
		l.Color = "00ff00"
		if err := c.UpdateLabel(ctx, l.Name, l); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("ListIssuesFilters", func(t *testing.T) {
		c := newClient(t)
		a, b := uniq+"-x", uniq+"-y"
		i1 := mustIssue(t, c, "both", a, b)
		_ = mustIssue(t, c, "only-a", a)
		i3 := mustIssue(t, c, "closed", a, b)
		closed := github.StateClosed
		if err := c.UpdateIssue(ctx, i3.Number, github.IssueUpdate{State: &closed}); err != nil {
			t.Fatal(err)
		}
		open, err := c.ListIssues(ctx, github.ListIssuesOpts{Labels: []string{a, b}})
		if err != nil {
			t.Fatal(err)
		}
		if nums(open) != fmt.Sprint([]int{i1.Number}) {
			t.Errorf("open AND = %s, want [%d]", nums(open), i1.Number)
		}
		all, _ := c.ListIssues(ctx, github.ListIssuesOpts{Labels: []string{a, b}, State: github.StateAll})
		if len(all) != 2 {
			t.Errorf("state=all returned %d, want 2", len(all))
		}
		since, _ := c.ListIssues(ctx, github.ListIssuesOpts{Labels: []string{a}, Since: time.Now().Add(time.Hour)})
		if len(since) != 0 {
			t.Errorf("future since returned %d", len(since))
		}
		capped, _ := c.ListIssues(ctx, github.ListIssuesOpts{Labels: []string{a}, State: github.StateAll, Max: 2})
		if len(capped) != 2 {
			t.Errorf("Max=2 returned %d", len(capped))
		}
	})

	t.Run("IssuesAndPRsShareNumbers", func(t *testing.T) {
		if cfg.newPR == nil {
			t.Skip("no PR factory")
		}
		c := newClient(t)
		is := mustIssue(t, c, "first")
		pr, _ := cfg.newPR(t, c)
		is2 := mustIssue(t, c, "third")
		if pr <= is.Number || is2.Number <= pr {
			t.Errorf("numbers not from one sequence: issue %d, pr %d, issue %d", is.Number, pr, is2.Number)
		}
		list, _ := c.ListIssues(ctx, github.ListIssuesOpts{State: github.StateAll})
		for _, x := range list {
			if x.IsPR {
				t.Errorf("PR #%d leaked into ListIssues by default", x.Number)
			}
		}
		withPRs, _ := c.ListIssues(ctx, github.ListIssuesOpts{State: github.StateAll, IncludePRs: true})
		found := false
		for _, x := range withPRs {
			found = found || (x.Number == pr && x.IsPR)
		}
		if !found {
			t.Errorf("PR #%d missing with IncludePRs", pr)
		}
	})

	t.Run("Search", func(t *testing.T) {
		c := newClient(t)
		req, st, guard := uniq+":req", uniq+":req:running", uniq+":hold"
		want := mustIssue(t, c, "match", req)
		mustIssue(t, c, "has-state", req, st)
		mustIssue(t, c, "has-guard", req, guard)
		mustIssue(t, c, "no-req", st)
		q := fmt.Sprintf(`is:open label:%q -label:%q -label:%s`, req, st, guard)
		var got []github.Issue
		poll(t, func() bool {
			var err error
			got, err = c.SearchIssues(ctx, q, github.SearchOpts{})
			if err != nil {
				t.Fatal(err)
			}
			return len(got) >= 1
		})
		if nums(got) != fmt.Sprint([]int{want.Number}) {
			t.Errorf("search = %s, want [%d]", nums(got), want.Number)
		}
	})

	t.Run("CloseNotPlannedThenReopen", func(t *testing.T) {
		c := newClient(t)
		is := mustIssue(t, c, "close me")
		closed, open := github.StateClosed, github.StateOpen
		np := github.ReasonNotPlanned
		if err := c.UpdateIssue(ctx, is.Number, github.IssueUpdate{State: &closed, StateReason: &np}); err != nil {
			t.Fatal(err)
		}
		got, _ := c.GetIssue(ctx, is.Number)
		if got.State != github.StateClosed || got.StateReason != github.ReasonNotPlanned || got.ClosedAt == nil {
			t.Errorf("after close: %+v", got)
		}
		if err := c.UpdateIssue(ctx, is.Number, github.IssueUpdate{State: &open}); err != nil {
			t.Fatal(err)
		}
		got, _ = c.GetIssue(ctx, is.Number)
		if got.State != github.StateOpen || got.ClosedAt != nil || got.StateReason == github.ReasonNotPlanned {
			t.Errorf("after reopen: %+v", got)
		}
		// Default reason is completed.
		is2 := mustIssue(t, c, "close default")
		if err := c.UpdateIssue(ctx, is2.Number, github.IssueUpdate{State: &closed}); err != nil {
			t.Fatal(err)
		}
		got, _ = c.GetIssue(ctx, is2.Number)
		if got.StateReason != github.ReasonCompleted {
			t.Errorf("default reason = %q, want completed", got.StateReason)
		}
	})

	t.Run("UpdateTitleBody", func(t *testing.T) {
		c := newClient(t)
		is := mustIssue(t, c, "old")
		nt, nb := "new", "body"
		if err := c.UpdateIssue(ctx, is.Number, github.IssueUpdate{Title: &nt, Body: &nb}); err != nil {
			t.Fatal(err)
		}
		got, _ := c.GetIssue(ctx, is.Number)
		if got.Title != nt || got.Body != nb {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("CommentThenList", func(t *testing.T) {
		c := newClient(t)
		is := mustIssue(t, c, "comments")
		cm, err := c.CreateComment(ctx, is.Number, "hello")
		if err != nil {
			t.Fatal(err)
		}
		if cm.ID == 0 || cm.Body != "hello" {
			t.Errorf("comment = %+v", cm)
		}
		list, err := c.ListComments(ctx, is.Number, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		if len(list) != 1 || list[0].Body != "hello" {
			t.Errorf("list = %+v", list)
		}
		id, _ := c.Identity(ctx)
		if list[0].Author != id.Login {
			t.Errorf("author = %q, want %q", list[0].Author, id.Login)
		}
	})

	t.Run("TimelineOrderAndActor", func(t *testing.T) {
		c := newClient(t)
		is := mustIssue(t, c, "timeline")
		l := uniq + "-tl"
		if err := c.AddLabels(ctx, is.Number, l); err != nil {
			t.Fatal(err)
		}
		if _, err := c.CreateComment(ctx, is.Number, "c"); err != nil {
			t.Fatal(err)
		}
		if err := c.RemoveLabel(ctx, is.Number, l); err != nil {
			t.Fatal(err)
		}
		tl, err := c.ListTimeline(ctx, is.Number)
		if err != nil {
			t.Fatal(err)
		}
		var kinds []string
		for _, ev := range tl {
			kinds = append(kinds, ev.Event)
		}
		want := "labeled commented unlabeled"
		if got := strings.Join(kinds, " "); got != want {
			t.Errorf("events = %q, want %q", got, want)
		}
		id, _ := c.Identity(ctx)
		for _, ev := range tl {
			if ev.Actor != id.Login {
				t.Errorf("%s actor = %q, want %q", ev.Event, ev.Actor, id.Login)
			}
		}
		if !sort.SliceIsSorted(tl, func(i, j int) bool { return tl[i].CreatedAt.Before(tl[j].CreatedAt) }) {
			t.Error("timeline not oldest first")
		}
	})

	t.Run("MergeSHAGuard", func(t *testing.T) {
		if cfg.newPR == nil {
			t.Skip("no PR factory")
		}
		c := newClient(t)
		n, sha := cfg.newPR(t, c)
		_, err := c.MergePR(ctx, n, github.MergeInput{SHA: "0000000000000000000000000000000000000000"})
		if !errors.Is(err, github.ErrConflict) {
			t.Fatalf("stale SHA err = %v, want ErrConflict", err)
		}
		res, err := c.MergePR(ctx, n, github.MergeInput{SHA: sha})
		if err != nil {
			t.Fatal(err)
		}
		if res.SHA == "" {
			t.Error("empty merge SHA")
		}
		pr, err := c.GetPR(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		if !pr.Merged || pr.State != github.StateClosed {
			t.Errorf("after merge: %+v", pr)
		}
	})
}

func mustIssue(t *testing.T, c github.Client, title string, labels ...string) github.Issue {
	t.Helper()
	is, err := c.CreateIssue(context.Background(), github.NewIssue{Title: title, Labels: labels})
	if err != nil {
		t.Fatal(err)
	}
	return is
}

func countEvents(t *testing.T, c github.Client, n int, event, label string) int {
	t.Helper()
	tl, err := c.ListTimeline(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	cnt := 0
	for _, ev := range tl {
		if ev.Event == event && ev.Label == label {
			cnt++
		}
	}
	return cnt
}

func nums(is []github.Issue) string {
	out := []int{}
	for _, i := range is {
		out = append(out, i.Number)
	}
	sort.Ints(out)
	return fmt.Sprint(out)
}

// poll retries f for up to 30s; search indexing on real GitHub lags.
func poll(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !f() && time.Now().Before(deadline) {
		time.Sleep(time.Second)
	}
}
