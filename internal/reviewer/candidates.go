package reviewer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/github/query"
	"github.com/albertcbrown/ard/internal/labels"
)

// candidate is a PR a reviewer role may claim, with the fresh re-read of the
// PR and its issue.
type candidate struct {
	role  config.Role
	pr    github.PullRequest
	issue github.Issue
	since time.Time
	// priority is the higher of the PR's and its issue's labels.Priority.
	priority int
}

// Skip reasons, fixed text for the review.skip log line.
const (
	skipNotAppPR      = "not_app_pr"
	skipNotWorkerRef  = "not_worker_branch"
	skipBaseNotMain   = "base_not_main"
	skipIssueClosed   = "issue_closed"
	skipIssueBlocked  = "issue_blocked"
	skipWorkerRunning = "worker_running"
)

// excludedLabels are the labels that keep a PR out of a reviewer role's
// queries: its own states and the guards.
func excludedLabels(role string) []string {
	return []string{
		labels.Running(labels.StageReview, role), labels.Done(labels.StageReview, role), labels.Changes(role),
		labels.ExecutorFailed, labels.NeedsHuman, labels.Hold, labels.TestsFailed,
	}
}

// candidates finds, per reviewer role, the PRs that request it and pass every
// check. A review job of another role on the same PR does not exclude it:
// reviewers run in parallel and their commits serialise on the branch lane.
func (t *tick) candidates(ctx context.Context) ([]candidate, []error) {
	l := t.l
	running, err := l.d.Store.RunningJobs(ctx)
	if err != nil {
		return nil, []error{fmt.Errorf("review: list running jobs: %w", err)}
	}
	workerBusy := map[int]bool{}
	for _, j := range running {
		if j.Stage == "worker" {
			workerBusy[j.Issue] = true
		}
	}
	var out []candidate
	var errs []error
	for _, role := range t.cfg.RolesOf(config.KindReviewer) {
		q, err := query.RoleQuery{
			Type: query.TypePR, Request: labels.Request(labels.StageReview, role.Name),
			States: []string{labels.Running(labels.StageReview, role.Name), labels.Done(labels.StageReview, role.Name), labels.Changes(role.Name)},
			Guards: []string{labels.ExecutorFailed, labels.NeedsHuman, labels.Hold, labels.TestsFailed},
		}.Build()
		if err != nil {
			errs = append(errs, fmt.Errorf("review role %s: %w", role.Name, err))
			continue
		}
		hits, err := l.d.Reader.SearchIssues(ctx, q, github.SearchOpts{Sort: "created", Order: "asc", Max: 50})
		if err != nil {
			errs = append(errs, fmt.Errorf("review role %s: search: %w", role.Name, err))
			continue
		}
		// A PR labelled P0 or P1 beyond the oldest 50 would never be seen, so
		// search for those too. A P0 label only on the linked issue is not
		// searched for: that gap is accepted (see doc.go).
		hq, qerr := query.RoleQuery{
			Type: query.TypePR, Request: labels.Request(labels.StageReview, role.Name),
			States: []string{labels.Running(labels.StageReview, role.Name), labels.Done(labels.StageReview, role.Name), labels.Changes(role.Name)},
			Guards: []string{labels.ExecutorFailed, labels.NeedsHuman, labels.Hold, labels.TestsFailed},
			AnyOf:  []string{labels.P0, labels.P1},
		}.Build()
		if qerr == nil {
			var more []github.Issue
			if more, qerr = l.d.Reader.SearchIssues(ctx, hq, github.SearchOpts{Sort: "created", Order: "asc", Max: 50}); qerr == nil {
				hits = mergeHits(hits, more)
			}
		}
		if qerr != nil {
			t.log.Warn("review.priority_search_failed", "role", role.Name, "error", qerr)
		}
		for _, hit := range hits {
			c, ok, err := t.consider(ctx, role, hit, workerBusy)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if ok {
				out = append(out, c)
			}
		}
	}
	return out, errs
}

func (t *tick) skip(pr int, role, reason string) {
	t.log.Info("review.skip", "pr", pr, "role", role, "reason", reason)
}

// consider re-reads PR hit.Number (search is eventually consistent) and its
// issue, then applies the rules in the package comment.
func (t *tick) consider(ctx context.Context, role config.Role, hit github.Issue, workerBusy map[int]bool) (candidate, bool, error) {
	l := t.l
	n := hit.Number
	pr, err := l.d.Reader.GetPR(ctx, n)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			return candidate{}, false, nil
		}
		return candidate{}, false, fmt.Errorf("review PR %d: re-read: %w", n, err)
	}
	if pr.State != github.StateOpen || pr.Merged || !hasLabel(pr.Labels, labels.Request(labels.StageReview, role.Name)) ||
		hasAny(pr.Labels, excludedLabels(role.Name)) {
		return candidate{}, false, nil
	}
	if !strings.EqualFold(pr.Author, t.self) {
		t.skip(n, role.Name, skipNotAppPR)
		return candidate{}, false, nil
	}
	if pr.BaseRef != "main" {
		t.skip(n, role.Name, skipBaseNotMain)
		return candidate{}, false, nil
	}
	_, issueN, ok := parseWorkerBranch(pr.HeadRef)
	if !ok {
		t.skip(n, role.Name, skipNotWorkerRef)
		return candidate{}, false, nil
	}
	is, err := l.d.Reader.GetIssue(ctx, issueN)
	if err != nil {
		if errors.Is(err, github.ErrNotFound) {
			t.skip(n, role.Name, skipIssueClosed)
			return candidate{}, false, nil
		}
		return candidate{}, false, fmt.Errorf("review PR %d: read issue %d: %w", n, issueN, err)
	}
	switch {
	case is.State != github.StateOpen || is.IsPR:
		t.skip(n, role.Name, skipIssueClosed)
		return candidate{}, false, nil
	case hasLabel(is.Labels, labels.NeedsHuman) || hasLabel(is.Labels, labels.Hold):
		t.skip(n, role.Name, skipIssueBlocked)
		return candidate{}, false, nil
	case workerBusy[issueN]:
		t.skip(n, role.Name, skipWorkerRunning)
		return candidate{}, false, nil
	}
	ok, err = t.recheck().Admitted(ctx, is)
	if err != nil || !ok {
		return candidate{}, false, err
	}
	return candidate{role: role, pr: pr, issue: is, since: hit.CreatedAt,
		priority: labels.Priority(append(append([]string(nil), is.Labels...), pr.Labels...))}, true, nil
}

func hasAny(have []string, want []string) bool {
	for _, w := range want {
		if hasLabel(have, w) {
			return true
		}
	}
	return false
}

func hasLabel(have []string, want string) bool {
	for _, h := range have {
		if strings.EqualFold(h, want) {
			return true
		}
	}
	return false
}

// mergeHits appends the hits of more that are not already in hits.
func mergeHits(hits, more []github.Issue) []github.Issue {
	seen := make(map[int]bool, len(hits))
	for _, h := range hits {
		seen[h.Number] = true
	}
	for _, h := range more {
		if !seen[h.Number] {
			seen[h.Number] = true
			hits = append(hits, h)
		}
	}
	return hits
}
