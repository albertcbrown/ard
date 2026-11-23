package reviewer

import (
	"context"
	"strings"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/prgate"
)

// sweepReady re-checks the open draft worker PRs, so a PR whose last review
// finished before a restart still gets marked. Errors are logged, never
// returned: a failed sweep must not fail the tick.
func (t *tick) sweepReady(ctx context.Context) {
	if len(t.cfg.RolesOf(config.KindReviewer)) == 0 {
		return
	}
	seen := map[int]bool{}
	for _, role := range t.cfg.RolesOf(config.KindReviewer) {
		q := `is:pr is:open label:"` + labels.Done(labels.StageReview, role.Name) + `"`
		hits, err := t.l.d.Reader.SearchIssues(ctx, q, github.SearchOpts{Sort: "created", Order: "asc", Max: 50})
		if err != nil {
			t.log.Warn("review.ready_sweep_failed", "role", role.Name, "error", err)
			continue
		}
		for _, hit := range hits {
			if seen[hit.Number] {
				continue
			}
			seen[hit.Number] = true
			t.maybeReady(ctx, hit.Number)
		}
	}
}

// maybeReady marks PR prNumber ready for review when every requested review
// is done at the PR's current head and nothing blocks it. It never converts a
// ready PR back to draft and never merges.
func (t *tick) maybeReady(ctx context.Context, prNumber int) {
	d := t.l.d
	pr, err := d.Reader.GetPR(ctx, prNumber)
	if err != nil {
		t.log.Warn("review.ready_check_failed", "pr", prNumber, "step", "get_pr", "error", err)
		return
	}
	if pr.State != github.StateOpen || pr.Merged || !pr.Draft || !strings.EqualFold(pr.Author, t.self) || pr.BaseRef != "main" {
		return
	}
	_, issue, ok := prgate.ParseWorkerBranch(pr.HeadRef)
	if !ok {
		return
	}
	var roleNames []string
	for _, role := range t.cfg.RolesOf(config.KindReviewer) {
		roleNames = append(roleNames, role.Name)
	}
	v := prgate.Labels(pr.Labels, roleNames)
	if !v.OK {
		return
	}
	reviewed, err := prgate.ReviewedHeads(ctx, d.Store, issue, v.Roles)
	if err != nil {
		t.log.Warn("review.ready_check_failed", "pr", prNumber, "step", "events", "error", err)
		return
	}
	var fetched bool
	for _, role := range v.Roles {
		head := reviewed[role]
		if head == "" {
			t.log.Debug("review.ready_head_unknown", "pr", prNumber, "role", role)
			return
		}
		if head == pr.HeadSHA {
			continue
		}
		if !fetched {
			spec := "+refs/heads/" + pr.HeadRef + ":" + localRef(pr.Number)
			if err := d.Git.FetchRef(ctx, d.RepoDir, d.RemoteURL, spec, d.Token); err != nil {
				t.log.Warn("review.ready_check_failed", "pr", prNumber, "step", "fetch", "error", err)
				return
			}
			fetched = true
		}
		covered, _, err := prgate.Covers(ctx, d.Git, d.RepoDir, head, pr.HeadSHA, "", issue)
		if err != nil {
			t.log.Warn("review.ready_check_failed", "pr", prNumber, "step", "diff", "error", err)
			return
		}
		if !covered {
			t.log.Debug("review.ready_head_moved", "pr", prNumber, "role", role)
			return
		}
	}
	if err := d.Effects.MarkReady(ctx, prNumber); err != nil {
		t.log.Warn("review.mark_ready_failed", "pr", prNumber, "issue", issue, "error", err)
		return
	}
	if t.dry {
		t.log.Info("review.would_mark_ready", "pr", prNumber, "issue", issue, "head", pr.HeadSHA)
		return
	}
	t.log.Info("review.marked_ready", "pr", prNumber, "issue", issue, "head", pr.HeadSHA)
}
