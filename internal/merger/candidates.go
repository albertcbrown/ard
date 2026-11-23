package merger

import (
	"context"
	"errors"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/prgate"
)

func parseBranch(ref string) (string, int, bool) { return prgate.ParseWorkerBranch(ref) }

// facts are what the gates read about one PR after the cheap checks passed.
type facts struct {
	pr      github.PullRequest
	issue   int
	jobIDs  []string
	reviews prgate.Verdict
	base    string
}

// recheck re-reads PR pr and its issue and applies the cheap gates: still
// open, ready, ours, not blocked, issue open and not blocked, no running
// worker or review job. ok is false when the PR is not mergeable this tick.
func (t *tick) recheck(ctx context.Context, listed github.PullRequest) (pr github.PullRequest, issue int, ok bool) {
	d := t.l.d
	pr, err := d.Reader.GetPR(ctx, listed.Number)
	if err != nil {
		if !errors.Is(err, github.ErrNotFound) {
			t.fail(listed.Number, "get_pr", err)
		}
		return pr, 0, false
	}
	_, issue, parsed := parseBranch(pr.HeadRef)
	switch {
	case pr.State != github.StateOpen || pr.Merged:
		t.skip(pr.Number, "not_open")
	case pr.Draft:
		t.skip(pr.Number, "draft")
	case pr.BaseRef != "main" || !parsed:
		t.skip(pr.Number, "not_worker_pr")
	case prgate.Blocked(pr.Labels):
		t.skip(pr.Number, "blocked_label")
	default:
		ok = true
	}
	if !ok {
		return pr, 0, false
	}
	is, err := d.Reader.GetIssue(ctx, issue)
	if err != nil {
		if !errors.Is(err, github.ErrNotFound) {
			t.fail(pr.Number, "get_issue", err)
		}
		return pr, 0, false
	}
	if is.State != github.StateOpen || is.IsPR || prgate.HasAny(is.Labels, []string{labels.Hold, labels.NeedsHuman}) {
		t.skip(pr.Number, "issue_closed_or_blocked")
		return pr, 0, false
	}
	if t.busy == nil {
		jobs, err := d.Store.RunningJobs(ctx)
		if err != nil {
			t.fail(pr.Number, "running_jobs", err)
			return pr, 0, false
		}
		t.busy = map[int]bool{}
		for _, j := range jobs {
			if j.Stage == "worker" || j.Stage == "review" {
				t.busy[j.Issue] = true
			}
		}
	}
	if t.busy[issue] {
		t.skip(pr.Number, "job_running")
		return pr, 0, false
	}
	return pr, issue, true
}

// reviewerRoles are the configured reviewer role names.
func (t *tick) reviewerRoles() []string {
	var out []string
	for _, r := range t.cfg.RolesOf(config.KindReviewer) {
		out = append(out, r.Name)
	}
	return out
}
