package reviewer

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/albertcbrown/ard/internal/config"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/state"
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
	_, issue, ok := parseWorkerBranch(pr.HeadRef)
	if !ok {
		return
	}
	if hasAny(pr.Labels, []string{labels.NeedsHuman, labels.Hold, labels.TestsFailed, labels.ExecutorFailed}) {
		return
	}
	var requested []string
	for _, role := range t.cfg.RolesOf(config.KindReviewer) {
		if !hasLabel(pr.Labels, labels.Request(labels.StageReview, role.Name)) {
			continue
		}
		if !hasLabel(pr.Labels, labels.Done(labels.StageReview, role.Name)) ||
			hasLabel(pr.Labels, labels.Changes(role.Name)) ||
			hasLabel(pr.Labels, labels.Running(labels.StageReview, role.Name)) {
			return
		}
		requested = append(requested, role.Name)
	}
	if len(requested) == 0 {
		return
	}
	reviewed, err := t.reviewedHeads(ctx, issue, requested)
	if err != nil {
		t.log.Warn("review.ready_check_failed", "pr", prNumber, "step", "events", "error", err)
		return
	}
	var fetched bool
	for _, role := range requested {
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
		changed, err := d.Git.CodeChanged(ctx, d.RepoDir, head, pr.HeadSHA, issueDir(issue))
		if err != nil {
			t.log.Warn("review.ready_check_failed", "pr", prNumber, "step", "diff", "error", err)
			return
		}
		if len(changed) > 0 {
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

// reviewedHeads returns, per role, the head recorded by its newest claim.
func (t *tick) reviewedHeads(ctx context.Context, issue int, roles []string) (map[string]string, error) {
	evs, err := t.l.d.Store.Events(ctx, state.EventFilter{Kind: state.EventClaim})
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, r := range roles {
		want[r] = true
	}
	out := map[string]string{}
	for _, e := range evs { // ascending: later claims overwrite
		if e.Issue != issue || e.Stage != stageReview || !want[e.Role] {
			continue
		}
		var data struct {
			Head string `json:"head"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.Head != "" {
			out[e.Role] = data.Head
		}
	}
	return out, nil
}
