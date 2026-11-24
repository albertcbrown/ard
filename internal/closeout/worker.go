package closeout

import (
	"fmt"
	"strings"

	"github.com/albertcbrown/ard/internal/labels"
)

// TestResult is how a worker's test run ended.
type TestResult string

// Test results.
const (
	TestsPassed TestResult = "passed"
	TestsFailed TestResult = "failed"
	TestsNone   TestResult = "none" // the role has no tests configured
)

// WorkerResult is what the worker loop knows that the role file cannot say:
// the pull request, the reviewers to request on it and how the tests went. The
// loop passes it through reaper.Finisher.FinishWith; it is persisted in the
// close-out record.
type WorkerResult struct {
	PR    int
	PRURL string
	// Reviewers are the reviewer roles to request on the PR, de-duplicated and
	// already limited to roles that exist. Close-out adds the role file's own
	// review requests after them.
	Reviewers []string
	Tests     TestResult
	Fixes     int    // fix attempts that ran
	TestNote  string // fixed, non-secret text, such as "ran out of time"
	// Hold, when set, turns the result into needs-human with this reason (a
	// diff guard rejection, for example). It never quotes file content.
	Hold string
	// Large, when set, is the diff guard's fixed note on a PR over the role's
	// diff_warn_lines ("Large change: N changed lines (warn at W)"). The PR
	// still proceeds; the note goes into the PR body and the issue comment.
	Large string
	// Fetched are the inputs the worker recorded under fetched: in its role
	// file. They come only from the parsed frontmatter, never from free text.
	Fetched []Fetched
}

// buildWorker maps a done worker role file and the loop's result to a plan.
func buildWorker(j Job, rf RoleFile, rs RoleSet) Plan {
	w := j.Worker
	switch {
	case w == nil:
		return needsHumanPlan(j, rf.Status, "worker ended without the loop's result")
	case w.Hold != "":
		return needsHumanPlan(j, rf.Status, w.Hold)
	case j.PR <= 0:
		return needsHumanPlan(j, rf.Status, "worker finished without a pull request")
	}
	n, pr := j.Issue, j.PR
	run := Label{n, labels.Running(labels.StageWorker, j.Role)}
	p := Plan{Status: rf.Status, Outcome: OutcomeDone}
	p.Terminal = []Label{{n, labels.Done(labels.StageWorker, j.Role)}}
	now, deferred, skipped := resolveNext(j, rf.Next, rs)
	p.Deferred, p.Skipped = deferred, skipped
	if w.Tests == TestsFailed {
		p.Terminal = append(p.Terminal, Label{n, labels.TestsFailed}, Label{pr, labels.TestsFailed})
		for _, r := range now {
			p.Skipped = append(p.Skipped, Skip{r.String(), "tests failed"})
		}
	} else {
		p.Remove = append(p.Remove, Label{n, labels.TestsFailed}, Label{pr, labels.TestsFailed})
		seen := map[string]bool{}
		add := func(role string) {
			if seen[role] {
				return
			}
			seen[role] = true
			p.Requests = append(p.Requests, Label{pr, labels.Request(labels.StageReview, role)})
		}
		for _, r := range w.Reviewers {
			if hasRole(rs, StageReview, r) {
				add(r)
			} else {
				p.Skipped = append(p.Skipped, Skip{"review:" + clip(r, 60), "no such role"})
			}
		}
		for _, r := range now {
			add(r.Role)
		}
	}
	p.Remove = append(p.Remove, run)
	if rf.Worker != "" {
		p.Skipped = append(p.Skipped, Skip{"worker:" + clip(rf.Worker, 60), "wrong stage"})
	}
	return p
}

// workerLines are the PR and Tests lines of a worker's close-out comment.
func workerLines(j Job) []string {
	w := j.Worker
	if w == nil || w.Hold != "" {
		return nil
	}
	var out []string
	if j.PR > 0 {
		if strings.HasPrefix(w.PRURL, "https://github.com/") && !strings.ContainsAny(w.PRURL, " ()[]<>\n") {
			out = append(out, fmt.Sprintf("PR: [#%d](%s)", j.PR, w.PRURL))
		} else {
			out = append(out, fmt.Sprintf("PR: #%d", j.PR))
		}
	}
	fixes := ""
	switch w.Fixes {
	case 0:
	case 1:
		fixes = " after 1 fix attempt"
	default:
		fixes = fmt.Sprintf(" after %d fix attempts", w.Fixes)
	}
	switch w.Tests {
	case TestsPassed:
		out = append(out, "Tests: passed"+fixes)
	case TestsFailed:
		line := "Tests: failed" + fixes
		if w.TestNote != "" {
			line += " · " + sanitize(w.TestNote)
		}
		out = append(out, line)
	case TestsNone:
		out = append(out, "Tests: not configured")
	}
	if w.Large != "" {
		out = append(out, sanitize(w.Large))
	}
	return out
}
