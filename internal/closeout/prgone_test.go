package closeout

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/github"
)

// goneEnv seeds PR 10 in the given state and returns an env whose review job
// targets it, with the PR check wired in.
func goneEnv(t *testing.T, st github.State, merged bool, verdict string) (*env, Job) {
	t.Helper()
	e := newEnv(t)
	e.pr = 10
	e.gh.SeedPR(github.PullRequest{Number: 10, Title: "p", State: st, Merged: merged,
		Labels: []string{"ard:review:eng-review:running"}})
	e.d.PR = e.gh.GetPR
	file := reviewFile
	if verdict == VerdictApprove {
		file = strings.Replace(strings.Replace(reviewFile, "verdict: changes", "verdict: approve", 1), "blocking: true", "blocking: false", 1)
	}
	rc := addedHello()
	return e, e.reviewJob(file, rc)
}

func checkStale(t *testing.T, e *env, j Job, out Outcome, err error, wantCommits int) {
	t.Helper()
	if err != nil || out != OutcomeStale {
		t.Fatalf("Run = %s, %v; want stale", out, err)
	}
	if e.cm.calls != wantCommits {
		t.Errorf("commit calls = %d, want %d", e.cm.calls, wantCommits)
	}
	l := e.labels(10)
	for name := range l {
		if name == "ard:executor:failed" || strings.HasSuffix(name, ":running") || strings.HasSuffix(name, ":changes") || strings.HasSuffix(name, ":done") {
			t.Errorf("unexpected label %s in %v", name, l)
		}
	}
	if sj := e.sjob(j.ID); sj.Status == "failed" {
		t.Errorf("job status = %s", sj.Status)
	}
	if e.markerComments(10, j.ID) != 0 {
		t.Error("a plain comment was posted")
	}
}

func TestReviewPRMergedBranchDeleted(t *testing.T) {
	e, j := goneEnv(t, github.StateClosed, true, VerdictApprove)
	e.cm.err = committer.ErrBranchGone // would fire if the commit were tried
	out, err := New(e.d).Run(context.Background(), j)
	checkStale(t, e, j, out, err, 0)
	if e.markerReviews(10, j.ID) != 1 {
		t.Error("want the review posted")
	}
	if rs, _ := e.reviewCalls(); len(rs) != 1 || !strings.Contains(rs[0].Body, "merged or closed") {
		t.Errorf("reviews = %+v", rs)
	}
}

func TestReviewPRClosedUnmerged(t *testing.T) {
	e, j := goneEnv(t, github.StateClosed, false, VerdictApprove)
	out, err := New(e.d).Run(context.Background(), j)
	checkStale(t, e, j, out, err, 0)
}

func TestReviewBranchGoneOnOpenPR(t *testing.T) {
	e, j := goneEnv(t, github.StateOpen, false, VerdictApprove)
	e.cm.err = committer.ErrBranchGone
	out, err := New(e.d).Run(context.Background(), j)
	checkStale(t, e, j, out, err, 1)
}

func TestReviewPRGoneChangesVerdict(t *testing.T) {
	e, j := goneEnv(t, github.StateClosed, true, VerdictChanges)
	out, err := New(e.d).Run(context.Background(), j)
	checkStale(t, e, j, out, err, 0)
}

func TestReviewPRGonePostFails(t *testing.T) {
	e, j := goneEnv(t, github.StateClosed, true, VerdictChanges)
	e.gh.FailNext("CreateReview", errBoom)
	out, err := New(e.d).Run(context.Background(), j)
	checkStale(t, e, j, out, err, 0)
}

func TestReviewCommitOtherErrorStillFails(t *testing.T) {
	e, j := goneEnv(t, github.StateOpen, false, VerdictApprove)
	e.cm.err = errors.New("push rejected")
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeFailed {
		t.Fatalf("Run = %s, %v; want failed", out, err)
	}
	if !e.labels(10)["ard:executor:failed"] {
		t.Error("want ard:executor:failed")
	}
}

func TestReviewGetPRErrorFallsThrough(t *testing.T) {
	e, j := goneEnv(t, github.StateOpen, false, VerdictChanges)
	e.d.PR = func(context.Context, int) (github.PullRequest, error) { return github.PullRequest{}, errBoom }
	out, err := New(e.d).Run(context.Background(), j)
	if err != nil || out != OutcomeChanges || e.cm.calls != 1 {
		t.Fatalf("Run = %s, %v, commits %d", out, err, e.cm.calls)
	}
}
