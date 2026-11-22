package closeout

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/gitx"
)

func fm(extra string) string {
	return "---\nrole: r\nstatus: done\nattempt: 1\nverdict: changes\n" + extra + "---\nSummary.\n"
}

func TestParseFindings(t *testing.T) {
	many := strings.Repeat("  - {file: a.go, severity: low, message: m}\n", 51)
	tests := []struct {
		name    string
		front   string
		want    []Finding
		wantErr string
	}{
		{name: "valid", front: "findings:\n  - file: a/b.go\n    line: 42\n    severity: high\n    blocking: true\n    message: The loop skips the last element.\n  - file: c_test.go\n    severity: info\n    message: No test.\n",
			want: []Finding{{File: "a/b.go", Line: 42, Severity: "high", Blocking: true, Message: "The loop skips the last element."},
				{File: "c_test.go", Severity: "info", Message: "No test."}}},
		{name: "empty list", front: "findings: []\n"},
		{name: "line string", front: "findings:\n  - {file: a.go, line: \"4\", severity: low, message: m}\n", wantErr: "frontmatter"},
		{name: "blocking string", front: "findings:\n  - {file: a.go, severity: low, blocking: \"yes\", message: m}\n", wantErr: "wrong type"},
		{name: "severity int", front: "findings:\n  - {file: a.go, severity: 3, message: m}\n", wantErr: "wrong type"},
		{name: "finding is a string", front: "findings:\n  - oops\n", wantErr: "frontmatter"},
		{name: "unknown key", front: "findings:\n  - {file: a.go, severity: low, message: m, extra: 1}\n", wantErr: "extra"},
		{name: "51 findings", front: "findings:\n" + many, wantErr: "51 findings"},
		{name: "bad severity", front: "findings:\n  - {file: a.go, severity: urgent, message: m}\n", wantErr: "severity"},
		{name: "absolute path", front: "findings:\n  - {file: /abs, severity: low, message: m}\n", wantErr: "absolute"},
		{name: "dotdot", front: "findings:\n  - {file: a/../b, severity: low, message: m}\n", wantErr: "clean relative"},
		{name: "backslash", front: "findings:\n  - {file: 'a\\b', severity: low, message: m}\n", wantErr: "backslash"},
		{name: "double slash", front: "findings:\n  - {file: a//b, severity: low, message: m}\n", wantErr: "clean relative"},
		{name: "empty path", front: "findings:\n  - {file: '', severity: low, message: m}\n", wantErr: "empty"},
		{name: "long path", front: "findings:\n  - {file: " + strings.Repeat("a", 301) + ", severity: low, message: m}\n", wantErr: "max 300"},
		{name: "missing message", front: "findings:\n  - {file: a.go, severity: low}\n", wantErr: "missing message"},
		{name: "blank message", front: "findings:\n  - {file: a.go, severity: low, message: '  '}\n", wantErr: "empty message"},
		{name: "missing file", front: "findings:\n  - {severity: low, message: m}\n", wantErr: "missing file"},
		{name: "line zero", front: "findings:\n  - {file: a.go, line: 0, severity: low, message: m}\n", wantErr: "below 1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rf, err := Parse([]byte(fm(tc.front)))
			if tc.wantErr != "" {
				if !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(rf.Findings) != fmt.Sprint(tc.want) {
				t.Errorf("findings = %+v, want %+v", rf.Findings, tc.want)
			}
		})
	}
}

func TestCheckFindings(t *testing.T) {
	one := "findings:\n  - {file: a.go, severity: high, blocking: true, message: m}\n"
	tests := []struct {
		name    string
		job     Job
		in      string
		wantErr bool
	}{
		{name: "triage job", job: Job{Stage: StageTriage, Role: "r"}, in: strings.Replace(fm(one), "verdict: changes\n", "", 1), wantErr: true},
		{name: "worker job", job: Job{Stage: StageWorker, Role: "r"}, in: strings.Replace(fm(one), "verdict: changes\n", "", 1), wantErr: true},
		{name: "review changes", job: Job{Stage: StageReview, Role: "r"}, in: fm(one)},
		{name: "approve with blocking is not malformed", job: Job{Stage: StageReview, Role: "r"}, in: strings.Replace(fm(one), "changes", "approve", 1)},
		{name: "changes with none", job: Job{Stage: StageReview, Role: "r"}, in: fm("findings:\n  - {file: a.go, severity: low, message: m}\n")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rf, err := Parse([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if err := rf.check(tc.job); (err != nil) != tc.wantErr {
				t.Errorf("check = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestDowngradeVerdict(t *testing.T) {
	rf := RoleFile{Verdict: VerdictApprove, Findings: []Finding{{Blocking: true}}}
	if !rf.DowngradeVerdict() || rf.Verdict != VerdictChanges {
		t.Errorf("rf = %+v", rf)
	}
	for _, rf := range []RoleFile{
		{Verdict: VerdictApprove, Findings: []Finding{{}}},
		{Verdict: VerdictChanges, Findings: []Finding{{Blocking: true}}},
	} {
		if rf.DowngradeVerdict() {
			t.Errorf("downgraded %+v", rf)
		}
	}
}

const reviewFile = `---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: hello.go
    line: 2
    severity: high
    blocking: true
    message: The loop skips the last element.
  - file: hello.go
    line: 9
    severity: low
    message: Context line, not added.
  - file: README.md
    line: 1
    severity: medium
    message: Docs are stale.
  - file: hello.go
    severity: info
    message: Whole-file remark.
---
Two problems; one blocks.
`

func (e *env) reviewJob(file string, rc *ReviewContext) Job {
	e.t.Helper()
	e.write(".ard/7/review-eng-review.md", file)
	j := e.claim(StageReview, "eng-review")
	j.Review = rc
	return j
}

func addedHello() *ReviewContext {
	return &ReviewContext{MergeBase: "mb", Added: map[string][]gitx.LineRange{"hello.go": {{From: 1, To: 4}}}}
}

func TestPrepareReviewPlan(t *testing.T) {
	ctx := context.Background()
	t.Run("split by added lines", func(t *testing.T) {
		e := newEnv(t)
		j := e.reviewJob(reviewFile, addedHello())
		rec, err := Prepare(ctx, j, e.d)
		if err != nil {
			t.Fatal(err)
		}
		rp := rec.Plan.Review
		if rec.Plan.Outcome != OutcomeChanges || rp == nil {
			t.Fatalf("plan = %+v", rec.Plan)
		}
		if rp.CommitSHA != j.BaseSHA || rp.Verdict != VerdictChanges || rp.NoLines {
			t.Errorf("review plan = %+v", rp)
		}
		if len(rp.Inline) != 1 || rp.Inline[0].Line != 2 {
			t.Errorf("inline = %+v", rp.Inline)
		}
		if len(rp.Other) != 3 {
			t.Errorf("other = %+v", rp.Other)
		}
	})
	t.Run("nil context puts everything in the body", func(t *testing.T) {
		e := newEnv(t)
		rec, err := Prepare(ctx, e.reviewJob(reviewFile, nil), e.d)
		if err != nil {
			t.Fatal(err)
		}
		rp := rec.Plan.Review
		if rp == nil || !rp.NoLines || len(rp.Inline) != 0 || len(rp.Other) != 4 {
			t.Errorf("review plan = %+v", rp)
		}
	})
	t.Run("a secret in a message is redacted", func(t *testing.T) {
		e := newEnv(t)
		file := strings.Replace(reviewFile, "Docs are stale.", "Key "+secretValue+" leaked.", 1)
		rec, err := Prepare(ctx, e.reviewJob(file, addedHello()), e.d)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Plan.Review == nil {
			t.Fatalf("plan = %+v", rec.Plan)
		}
		for _, f := range append(rec.Plan.Review.Inline, rec.Plan.Review.Other...) {
			if strings.Contains(f.Message, secretValue) {
				t.Errorf("message not redacted: %q", f.Message)
			}
		}
	})
	t.Run("approve with a blocking finding becomes changes", func(t *testing.T) {
		e := newEnv(t)
		file := strings.Replace(reviewFile, "verdict: changes", "verdict: approve", 1)
		rec, err := Prepare(ctx, e.reviewJob(file, addedHello()), e.d)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Plan.Outcome != OutcomeChanges || !rec.Plan.Review.Downgraded || rec.Plan.Review.Verdict != VerdictChanges {
			t.Errorf("plan = %+v review = %+v", rec.Plan, rec.Plan.Review)
		}
		if got := RenderReview(rec, github.Repo{Owner: "o", Name: "r"}, false, false); !strings.Contains(got, "treats it as changes requested") {
			t.Errorf("body lacks the downgrade note:\n%s", got)
		}
	})
	t.Run("approve on a moved head is stale, changes still labels", func(t *testing.T) {
		e := newEnv(t)
		rc := addedHello()
		rc.HeadMoved = true
		file := strings.Replace(reviewFile, "verdict: changes", "verdict: approve", 1)
		file = strings.Replace(file, "blocking: true", "blocking: false", 1)
		rec, err := Prepare(ctx, e.reviewJob(file, rc), e.d)
		if err != nil {
			t.Fatal(err)
		}
		if rec.Plan.Outcome != OutcomeStale || len(rec.Plan.Terminal) != 0 || rec.Plan.Review == nil || !rec.Plan.Review.HeadMoved {
			t.Fatalf("plan = %+v", rec.Plan)
		}
		if len(rec.Plan.Remove) != 1 || rec.Plan.Remove[0].Name != "ard:review:eng-review:running" {
			t.Errorf("remove = %+v", rec.Plan.Remove)
		}
		e2 := newEnv(t)
		rec2, err := Prepare(ctx, e2.reviewJob(reviewFile, rc), e2.d)
		if err != nil || rec2.Plan.Outcome != OutcomeChanges {
			t.Errorf("changes on a moved head: %+v, %v", rec2.Plan, err)
		}
	})
	t.Run("needs-human, failed and retry have no review", func(t *testing.T) {
		e := newEnv(t)
		e.write(".ard/7/review-eng-review.md", "---\nrole: eng-review\nstatus: needs-human\nattempt: 1\n---\nHelp.\n")
		j := e.claim(StageReview, "eng-review")
		j.Review = addedHello()
		rec, err := Prepare(ctx, j, e.d)
		if err != nil || rec.Plan.Outcome != OutcomeNeedsHuman || rec.Plan.Review != nil {
			t.Errorf("needs-human: %+v, %v", rec.Plan, err)
		}
		j.ExitCode = 3
		if rec, err = Prepare(ctx, j, e.d); err != nil || rec.Plan.Outcome != OutcomeFailed || rec.Plan.Review != nil {
			t.Errorf("failed: %+v, %v", rec.Plan, err)
		}
		j.ExitCode = 0
		j.Cause = Cause{Kind: CauseNoProcess, Transient: true, Retry: true}
		if rec, err = Prepare(ctx, j, e.d); err != nil || rec.Plan.Outcome != OutcomeRetry || rec.Plan.Review != nil {
			t.Errorf("retry: %+v, %v", rec.Plan, err)
		}
	})
}

func TestCloserPostsOneReview(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	j := e.reviewJob(reviewFile, addedHello())
	out, err := New(e.d).Run(ctx, j)
	if err != nil || out != OutcomeChanges {
		t.Fatalf("Run = %s, %v", out, err)
	}
	reviews, comments := e.reviewCalls()
	if len(reviews) != 1 || comments != 0 {
		t.Fatalf("reviews %d comments %d", len(reviews), comments)
	}
	r := reviews[0]
	if r.CommitSHA != j.BaseSHA || len(r.Comments) != 1 || r.Comments[0].Path != "hello.go" || r.Comments[0].Line != 2 || r.Comments[0].Side != "RIGHT" {
		t.Errorf("review = %+v", r)
	}
	if !strings.HasPrefix(r.Body, markerFor(j.ID)) || !strings.Contains(r.Body, "README.md:1") || !strings.Contains(r.Body, "Findings outside the changed lines:") {
		t.Errorf("body:\n%s", r.Body)
	}
	if !strings.HasPrefix(r.Comments[0].Body, "**high · blocking**\n\n") {
		t.Errorf("inline body = %q", r.Comments[0].Body)
	}
	if e.markerComments(9, j.ID) != 0 || e.markerComments(7, j.ID) != 0 {
		t.Error("a comment was posted")
	}
	// The record says how it went.
	rec, err := decode(e.sjob(j.ID))
	if err != nil || rec.ReviewMode != ReviewInline {
		t.Errorf("mode = %q, %v", rec.ReviewMode, err)
	}
}

func TestCloserReviewResumeAfterPost(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	j := e.reviewJob(reviewFile, addedHello())
	e.fstore.failAt = 4 // the save after the comment phase
	if _, err := New(e.d).Run(ctx, j); err == nil {
		t.Fatal("want an error")
	}
	if rs, _ := e.reviewCalls(); len(rs) != 1 {
		t.Fatalf("reviews = %d", len(rs))
	}
	e.fstore.failAt = 0
	if err := New(e.deps(0)).Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if rs, c := e.reviewCalls(); len(rs) != 1 || c != 0 {
		t.Errorf("replay posted again: reviews %d comments %d", len(rs), c)
	}
	if e.markerReviews(9, j.ID) != 1 {
		t.Error("want one review")
	}
}

func TestCloserReviewFallbacks(t *testing.T) {
	ctx := context.Background()
	unprocessable := &github.HTTPError{Status: 422, Message: "line must be part of the diff"}
	t.Run("one 422 gives a body-only review", func(t *testing.T) {
		e := newEnv(t)
		e.gh.FailNext("CreateReview", unprocessable)
		j := e.reviewJob(reviewFile, addedHello())
		if _, err := New(e.d).Run(ctx, j); err != nil {
			t.Fatal(err)
		}
		rs, c := e.reviewCalls()
		if len(rs) != 2 || c != 0 {
			t.Fatalf("reviews %d comments %d", len(rs), c)
		}
		if len(rs[1].Comments) != 0 || !strings.Contains(rs[1].Body, "hello.go:2") || !strings.Contains(rs[1].Body, "README.md:1") {
			t.Errorf("second review = %+v", rs[1])
		}
		if e.markerReviews(9, j.ID) != 1 {
			t.Error("want one stored review")
		}
	})
	t.Run("two 422s give a comment a resume finds", func(t *testing.T) {
		e := newEnv(t)
		e.gh.FailNext("CreateReview", unprocessable)
		e.gh.FailNext("CreateReview", unprocessable)
		j := e.reviewJob(reviewFile, addedHello())
		e.fstore.failAt = 4
		if _, err := New(e.d).Run(ctx, j); err == nil {
			t.Fatal("want an error")
		}
		if e.markerComments(9, j.ID) != 1 {
			t.Fatal("want the fallback comment")
		}
		e.fstore.failAt = 0
		if err := New(e.deps(0)).Resume(ctx); err != nil {
			t.Fatal(err)
		}
		if _, c := e.reviewCalls(); c != 1 {
			t.Errorf("comments = %d", c)
		}
	})
	t.Run("a non-422 error stays resumable and posts once", func(t *testing.T) {
		e := newEnv(t)
		e.gh.FailNext("CreateReview", errBoom)
		j := e.reviewJob(reviewFile, addedHello())
		if _, err := New(e.d).Run(ctx, j); err == nil {
			t.Fatal("want an error")
		}
		if sj := e.sjob(j.ID); sj.CloseoutPhase != string(PhaseRequested) {
			t.Errorf("phase = %s", sj.CloseoutPhase)
		}
		if err := New(e.deps(0)).Resume(ctx); err != nil {
			t.Fatal(err)
		}
		if e.markerReviews(9, j.ID) != 1 {
			t.Error("want one review after the resume")
		}
	})
	t.Run("a blocked review posts the ordinary comment", func(t *testing.T) {
		e := newEnv(t)
		e.write(".ard/7/review-eng-review.md", "---\nrole: eng-review\nstatus: blocked\nattempt: 1\n---\nCannot tell.\n")
		j := e.claim(StageReview, "eng-review")
		j.Review = addedHello()
		if _, err := New(e.d).Run(ctx, j); err != nil {
			t.Fatal(err)
		}
		if rs, c := e.reviewCalls(); len(rs) != 0 || c != 1 {
			t.Errorf("reviews %d comments %d", len(rs), c)
		}
	})
	t.Run("a stale approve posts a review and sets no pass label", func(t *testing.T) {
		e := newEnv(t)
		rc := addedHello()
		rc.HeadMoved = true
		j := e.reviewJob(strings.Replace(strings.Replace(reviewFile, "verdict: changes", "verdict: approve", 1), "blocking: true", "blocking: false", 1), rc)
		out, err := New(e.d).Run(ctx, j)
		if err != nil || out != OutcomeStale {
			t.Fatalf("Run = %s, %v", out, err)
		}
		l := e.labels(9)
		if l["ard:review:eng-review:done"] || l["ard:review:eng-review:running"] {
			t.Errorf("labels = %v", l)
		}
		if e.markerReviews(9, j.ID) != 1 {
			t.Error("want a review")
		}
	})
}

func TestRenderReviewBodyCap(t *testing.T) {
	var fs []Finding
	for i := 0; i < 50; i++ {
		fs = append(fs, Finding{File: "pkg/file.go", Line: i + 1, Severity: "low", Message: strings.Repeat("x", 400)})
	}
	rec := Record{Job: Job{ID: "J", Role: "r", Attempt: 1}, Plan: Plan{Summary: strings.Repeat("s\n", 3000),
		Review: &ReviewPlan{CommitSHA: "0123456789abcdef", Verdict: VerdictChanges, Other: fs}}}
	body := RenderReview(rec, github.Repo{Owner: "o", Name: "r"}, true, false)
	if n := len([]rune(body)); n > maxReviewRunes {
		t.Errorf("body is %d runes", n)
	}
	if !strings.Contains(body, "Reviewed commit `0123456789ab`") {
		t.Errorf("footer lost:\n%s", body[len(body)-200:])
	}
	// Few findings: no "and N more".
	rec.Plan.Review.Other = fs[:2]
	small := RenderReview(rec, github.Repo{Owner: "o", Name: "r"}, true, false)
	if strings.Contains(small, "more in the committed file") || strings.Count(small, "\n- ") != 2 {
		t.Errorf("small body:\n%s", small)
	}
	rec.Plan.Review.Other = fs
	rec.Plan.Review.NoLines = true
	if !strings.Contains(RenderReview(rec, github.Repo{}, true, false), "Findings (line placement unavailable):") {
		t.Error("NoLines heading missing")
	}
}

func TestRenderReviewGolden(t *testing.T) {
	rec := Record{Job: Job{ID: "J1", Role: "eng-review", Attempt: 1}, SHA: "abc",
		Plan: Plan{Summary: "Two problems; one blocks.", RolePath: ".ard/7/review-eng-review.md",
			Review: &ReviewPlan{CommitSHA: "0123456789abcdef0123", Verdict: VerdictChanges,
				Other: []Finding{{File: "README.md", Line: 1, Severity: "medium", Message: "Docs are stale.\nMore."}, {File: "a.go", Severity: "info", Blocking: true, Message: "Whole file @bob"}}}}}
	want := "<!-- ard:closeout job=J1 attempt=1 -->\n" +
		"**ard · review:eng-review · changes**\n\n" +
		"Two problems; one blocks.\n\n" +
		"Findings outside the changed lines:\n" +
		"- **medium · non-blocking** `README.md:1` Docs are stale.\n" +
		"- **info · blocking** `a.go` Whole file @​bob\n\n" +
		"Reviewed commit `0123456789ab`\n" +
		"Output: [.ard/7/review-eng-review.md](https://github.com/o/r/blob/abc/.ard/7/review-eng-review.md) · job `J1` · attempt 1\n"
	if got := RenderReview(rec, github.Repo{Owner: "o", Name: "r"}, false, false); got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}
