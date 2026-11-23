package fake_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/clienttest"
	"github.com/albertcbrown/ard/internal/github/fake"
)

func TestConformance(t *testing.T) {
	clienttest.Run(t,
		func(t *testing.T) github.Client { return fake.New() },
		clienttest.WithPR(func(t *testing.T, c github.Client) (int, string) {
			f := c.(*fake.Fake)
			n := f.SeedPR(github.PullRequest{Title: "pr", HeadRef: "feat", HeadSHA: "abc123", BaseRef: "main"})
			return n, "abc123"
		}),
		clienttest.WithPin(nil),
		clienttest.WithTransfer(func(t *testing.T, c github.Client, n int) { c.(*fake.Fake).TransferIssue(n) }),
	)
}

func TestTransferIssuePanicsOnPR(t *testing.T) {
	f := fake.New()
	n := f.SeedPR(github.PullRequest{Title: "pr"})
	for name, num := range map[string]int{"pr": n, "unknown": 999} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: want panic", name)
				}
			}()
			f.TransferIssue(num)
		}()
	}
}

func TestFailNextFiresOnce(t *testing.T) {
	f := fake.New()
	boom := errors.New("boom")
	f.FailNext("CreateIssue", boom)
	if _, err := f.CreateIssue(context.Background(), github.NewIssue{Title: "x"}); !errors.Is(err, boom) {
		t.Fatalf("first err = %v", err)
	}
	if _, err := f.CreateIssue(context.Background(), github.NewIssue{Title: "x"}); err != nil {
		t.Fatalf("second err = %v", err)
	}
}

func TestWritesOrderExcludesSeeds(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	n := f.SeedIssue(github.Issue{Title: "seed"})
	f.SeedComment(n, github.Comment{Body: "seed"})
	_ = f.AddLabels(ctx, n, "a")
	_, _ = f.CreateComment(ctx, n, "hi")
	_ = f.PinIssue(ctx, n)
	var got []string
	for _, c := range f.Writes() {
		got = append(got, c.Method)
	}
	if want := "AddLabels CreateComment PinIssue"; strings.Join(got, " ") != want {
		t.Errorf("writes = %v, want %s", got, want)
	}
	if !f.Pinned(n) {
		t.Error("issue not pinned")
	}
	// reads are not logged
	_, _ = f.GetIssue(ctx, n)
	if len(f.Writes()) != 3 {
		t.Error("read was logged as a write")
	}
}

func TestAsAttributesWrites(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	n := f.SeedIssue(github.Issue{Title: "x"})
	human := f.As("alice")
	if err := human.AddLabels(ctx, n, "ard:hold"); err != nil {
		t.Fatal(err)
	}
	tl, _ := f.ListTimeline(ctx, n)
	if len(tl) != 1 || tl[0].Actor != "alice" {
		t.Errorf("timeline = %+v", tl)
	}
	// Same state: the bot sees the label, and its own writes keep its identity.
	is, _ := f.GetIssue(ctx, n)
	if len(is.Labels) != 1 {
		t.Errorf("labels = %v", is.Labels)
	}
	_ = f.RemoveLabel(ctx, n, "ard:hold")
	tl, _ = f.ListTimeline(ctx, n)
	if tl[1].Actor != "ard-test[bot]" {
		t.Errorf("bot actor = %q", tl[1].Actor)
	}
}

func TestSearchRejectsUnknownTerms(t *testing.T) {
	f := fake.New()
	for _, q := range []string{"author:bob", "freetext", `label:"unterminated`, "is:merged"} {
		if _, err := f.SearchIssues(context.Background(), q, github.SearchOpts{}); err == nil {
			t.Errorf("query %q: want error", q)
		}
	}
}

func TestSearchOrderAndMax(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	for i := 0; i < 5; i++ {
		f.SeedIssue(github.Issue{Title: "i", Labels: []string{"x"}})
	}
	got, err := f.SearchIssues(ctx, "repo:o/r is:open label:x", github.SearchOpts{Order: "desc", Max: 2})
	if err != nil || len(got) != 2 || got[0].Number != 5 {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestMergeAndPRFlow(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	pr, err := f.CreatePR(ctx, github.NewPR{Title: "t", Head: "b", Base: "main", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.MergePR(ctx, pr.Number, github.MergeInput{SHA: pr.HeadSHA})
	if !errors.Is(err, github.ErrNotMergeable) {
		t.Errorf("draft merge err = %v", err)
	}
	if err := f.MarkReady(ctx, pr.Number); err != nil {
		t.Fatal(err)
	}
	if err := f.UpdateBranch(ctx, pr.Number, "stale"); !errors.Is(err, github.ErrConflict) {
		t.Errorf("update branch stale err = %v", err)
	}
	f.FailNext("UpdateBranch", github.ErrConflict)
	if err := f.UpdateBranch(ctx, pr.Number, pr.HeadSHA); !errors.Is(err, github.ErrConflict) {
		t.Errorf("injected conflict err = %v", err)
	}
	if err := f.UpdateBranch(ctx, pr.Number, pr.HeadSHA); err != nil {
		t.Fatal(err)
	}
	cur, _ := f.GetPR(ctx, pr.Number)
	if cur.HeadSHA == pr.HeadSHA {
		t.Error("head did not move")
	}
	if _, err := f.MergePR(ctx, pr.Number, github.MergeInput{SHA: cur.HeadSHA}); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteBranch(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	if err := f.DeleteBranch(ctx, "b"); err != nil {
		t.Errorf("second delete: %v", err)
	}
	if _, err := f.GetPR(ctx, 999); !errors.Is(err, github.ErrNotFound) {
		t.Errorf("missing PR err = %v", err)
	}
}

func TestPermissionDefaultsToNone(t *testing.T) {
	f := fake.New()
	p, _ := f.GetPermission(context.Background(), "nobody")
	if p.Level != "none" {
		t.Errorf("perm = %+v", p)
	}
	f.SetPermission("bob", github.Permission{Level: "write", Role: "write"})
	p, _ = f.GetPermission(context.Background(), "bob")
	if !p.AtLeastTriage() {
		t.Errorf("perm = %+v", p)
	}
}

func TestConcurrentWriters(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	n := f.SeedIssue(github.Issue{Title: "x"})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = f.AddLabels(ctx, n, fmt.Sprintf("l%d", i%5))
			_, _ = f.CreateComment(ctx, n, "c")
			_, _ = f.CreateIssue(ctx, github.NewIssue{Title: "y"})
			_, _ = f.GetIssue(ctx, n)
		}(i)
	}
	wg.Wait()
	is, _ := f.GetIssue(ctx, n)
	if len(is.Labels) != 5 {
		t.Errorf("labels = %v", is.Labels)
	}
	if got := len(f.Writes()); got != 60 {
		t.Errorf("writes = %d, want 60", got)
	}
}

func TestSearchInTitleText(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	f.SeedIssue(github.Issue{Title: "ard status"})
	f.SeedIssue(github.Issue{Title: "something else"})
	got, err := f.SearchIssues(ctx, `is:issue in:title "ARD Status"`, github.SearchOpts{})
	if err != nil || len(got) != 1 || got[0].Title != "ard status" {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := f.SearchIssues(ctx, "in:body", github.SearchOpts{}); err == nil {
		t.Error("in:body should be unsupported")
	}
	if _, err := f.SearchIssues(ctx, `"ard status"`, github.SearchOpts{}); err == nil {
		t.Error("quoted text without in:title should be rejected")
	}
}

func TestCreateReviewThenListAndFail(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	n := f.SeedPR(github.PullRequest{Title: "pr", HeadSHA: "abc"})
	in := github.ReviewInput{CommitSHA: "abc", Body: "body", Comments: []github.InlineComment{{Path: "a.go", Line: 2, Body: "c"}}}
	rid, err := f.CreateReview(ctx, n, in)
	if err != nil || rid == 0 {
		t.Fatal(rid, err)
	}
	rs, err := f.ListReviews(ctx, n)
	if err != nil || len(rs) != 1 || rs[0].Body != "body" || rs[0].State != "COMMENTED" || rs[0].CommitSHA != "abc" {
		t.Fatalf("reviews = %+v, %v", rs, err)
	}
	if cs, _ := f.ListReviewComments(ctx, n); len(cs) != 1 || cs[0].Path != "a.go" {
		t.Errorf("inline = %+v", cs)
	}
	f.FailNext("CreateReview", &github.HTTPError{Status: 422})
	var he *github.HTTPError
	if _, err := f.CreateReview(ctx, n, in); !errors.As(err, &he) || he.Status != 422 {
		t.Fatalf("err = %v", err)
	}
	if rs, _ := f.ListReviews(ctx, n); len(rs) != 1 {
		t.Errorf("a failed review was recorded: %+v", rs)
	}
	if _, err := f.ListReviews(ctx, 999); !errors.Is(err, github.ErrNotFound) {
		t.Errorf("non-PR err = %v", err)
	}
}

func TestRequiredChecksAndCheckRuns(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	got, err := f.RequiredChecks(ctx, "main")
	if err != nil || !got.Available || len(got.Checks) != 0 {
		t.Fatalf("default = %+v, %v", got, err)
	}
	f.SetRequiredChecks("main", github.RequiredCheck{Context: "build"})
	if got, _ = f.RequiredChecks(ctx, "main"); !got.Available || len(got.Checks) != 1 || got.Checks[0].Context != "build" {
		t.Errorf("seeded = %+v", got)
	}
	f.SetRulesUnavailable("main")
	if got, err = f.RequiredChecks(ctx, "main"); err != nil || got.Available {
		t.Errorf("unavailable = %+v, %v", got, err)
	}
	f.SetAppID("x", 5)
	if id, err := f.AppID(ctx, "x"); err != nil || id != 5 {
		t.Errorf("AppID = %d, %v", id, err)
	}
	if rs, err := f.ListCheckRuns(ctx, "s1"); err != nil || len(rs) != 0 {
		t.Errorf("default runs = %+v, %v", rs, err)
	}
	f.SetCheckRuns("s1", github.CheckRun{Name: "build", Status: "completed", Conclusion: "success"})
	if rs, _ := f.ListCheckRuns(ctx, "s1"); len(rs) != 1 || rs[0].Name != "build" {
		t.Errorf("seeded runs = %+v", rs)
	}
}

func TestUpdateBranchDirtyAndClean(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	n := f.SeedPR(github.PullRequest{Title: "p", HeadRef: "b", HeadSHA: "h1", BaseRef: "main"})
	f.SetMergeableState(n, "dirty")
	if err := f.UpdateBranch(ctx, n, "h1"); !errors.Is(err, github.ErrConflict) {
		t.Fatalf("dirty err = %v", err)
	}
	if pr, _ := f.GetPR(ctx, n); pr.HeadSHA != "h1" {
		t.Errorf("head moved on conflict: %s", pr.HeadSHA)
	}
	f.SetMergeableState(n, "behind")
	if err := f.UpdateBranch(ctx, n, "h1"); err != nil {
		t.Fatal(err)
	}
	if pr, _ := f.GetPR(ctx, n); pr.MergeableState != "clean" {
		t.Errorf("state = %q, want clean", pr.MergeableState)
	}
}

func TestMergeClosesLinkedIssues(t *testing.T) {
	ctx := context.Background()
	f := fake.New()
	a := f.SeedIssue(github.Issue{Title: "a", State: github.StateOpen})
	b := f.SeedIssue(github.Issue{Title: "b", State: github.StateOpen})
	other := f.SeedPR(github.PullRequest{Title: "o", HeadRef: "o", HeadSHA: "o1", BaseRef: "dev", Body: fmt.Sprintf("Closes #%d", b)})
	pr := f.SeedPR(github.PullRequest{Title: "p", HeadRef: "p", HeadSHA: "p1", BaseRef: "main", Body: fmt.Sprintf("Closes #%d\n\nmore", a)})
	if _, err := f.MergePR(ctx, other, github.MergeInput{SHA: "o1"}); err != nil {
		t.Fatal(err)
	}
	if is, _ := f.GetIssue(ctx, b); is.State != github.StateOpen {
		t.Errorf("non-main merge closed #%d", b)
	}
	if _, err := f.MergePR(ctx, pr, github.MergeInput{SHA: "p1"}); err != nil {
		t.Fatal(err)
	}
	is, _ := f.GetIssue(ctx, a)
	if is.State != github.StateClosed || is.StateReason != github.ReasonCompleted {
		t.Errorf("issue = %+v", is)
	}
	tl, _ := f.ListTimeline(ctx, a)
	if len(tl) == 0 || tl[len(tl)-1].Event != "closed" {
		t.Errorf("timeline = %+v", tl)
	}
}
