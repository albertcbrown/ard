package merger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/albertcbrown/ard/internal/agentjob"
	"github.com/albertcbrown/ard/internal/committer"
	"github.com/albertcbrown/ard/internal/github"
	"github.com/albertcbrown/ard/internal/labels"
	"github.com/albertcbrown/ard/internal/prgate"
	"github.com/albertcbrown/ard/internal/state"
)

func mergeRef(pr int) string { return "refs/ard/merge/" + strconv.Itoa(pr) }

// consider runs the gates of one PR in order and reports whether it merged
// (which ends the tick). Every path that does not merge logs why.
func (t *tick) consider(ctx context.Context, listed github.PullRequest) (merged bool) {
	d := t.l.d
	pr, issue, ok := t.recheck(ctx, listed)
	if !ok {
		return false
	}
	n := pr.Number
	roles := t.reviewerRoles()
	v := prgate.Labels(pr.Labels, roles)
	if !v.OK {
		t.skip(n, v.Reason)
		return false
	}

	// d. Pin the head: the branch tip must be the SHA the API reported.
	if err := d.Git.FetchRef(ctx, d.RepoDir, d.RemoteURL, "+refs/heads/"+pr.HeadRef+":"+mergeRef(n), d.Token); err != nil {
		t.fail(n, "fetch", err)
		return false
	}
	head, err := d.Git.RevParse(ctx, d.RepoDir, mergeRef(n))
	if err != nil {
		t.fail(n, "rev_parse", err)
		return false
	}
	if head != pr.HeadSHA {
		t.log.Info("merge.head_moved", "pr", n, "api_head", pr.HeadSHA, "branch_head", head)
		return false
	}
	graceOver := t.sawHead(n, head)
	base, err := d.Git.MergeBase(ctx, d.RepoDir, t.mainTip, head)
	if err != nil {
		t.fail(n, "merge_base", err)
		return false
	}

	// e. Every requested review covers this head.
	reviewed, err := prgate.ReviewedHeads(ctx, d.Store, issue, v.Roles)
	if err != nil {
		t.fail(n, "review_heads", err)
		return false
	}
	for _, role := range v.Roles {
		ok, broken, err := prgate.Covers(ctx, d.Git, d.RepoDir, reviewed[role], head, base)
		if err != nil {
			t.fail(n, "review_cover", err)
			return false
		}
		if !ok {
			t.log.Info("merge.reviews_stale", "pr", n, "issue", issue, "role", role, "reviewed", reviewed[role], "head", head, "paths", len(broken),
				"hint", "re-request the review; fix rounds (#41) will do this")
			return false
		}
	}

	// f. Closing reference and trailer.
	if !closingRef(pr.Body, issue) {
		return t.needsHuman(ctx, pr, "no_closing_ref", "")
	}
	jobIDs, err := t.workerJobs(ctx, issue, pr.HeadRef)
	if err != nil {
		t.fail(n, "worker_jobs", err)
		return false
	}
	if len(jobIDs) == 0 {
		return t.needsHuman(ctx, pr, "no_worker_job", "")
	}

	// g. Conflict. GitHub computes mergeable lazily: unknown means retry.
	if pr.Mergeable == nil || pr.MergeableState == "unknown" {
		t.log.Info("merge.mergeable_unknown", "pr", n)
		return false
	}
	if !*pr.Mergeable || pr.MergeableState == "dirty" {
		return t.needsHuman(ctx, pr, "conflict", "")
	}

	// h. Behind main.
	changed, err := d.Git.CodeChanged(ctx, d.RepoDir, base, t.mainTip)
	if err != nil {
		t.fail(n, "behind", err)
		return false
	}
	if behind(changed) {
		t.updateBranch(ctx, pr, issue)
		return false
	}

	// i. Checks.
	runs, err := d.Reader.ListCheckRuns(ctx, head)
	if err != nil {
		t.fail(n, "check_runs", err)
		return false
	}
	switch st, names := evalChecks(t.required, runs, graceOver); st {
	case checksPending:
		t.log.Info("merge.waiting_checks", "pr", n, "head", head, "checks", strings.Join(names, ","), "grace_over", graceOver)
		return false
	case checksRed:
		return t.needsHuman(ctx, pr, "checks_failed", checkList(names))
	}

	// j. Protected paths need a write-access approval of this head.
	files, err := d.Git.CodeChanged(ctx, d.RepoDir, base, head)
	if err != nil {
		t.fail(n, "changed_paths", err)
		return false
	}
	if patterns := t.protected(files); len(patterns) > 0 {
		ok, err := t.hasApproval(ctx, pr, head, base)
		if err != nil {
			t.fail(n, "approval", err)
			return false
		}
		if !ok {
			t.needApproval(ctx, pr, head, patterns)
			return false
		}
	}

	// k. Merge.
	return t.merge(ctx, pr, issue, head, jobIDs)
}

// workerJobs returns the ids of the issue's worker jobs that ran on branch,
// oldest first.
func (t *tick) workerJobs(ctx context.Context, issue int, branch string) ([]string, error) {
	jobs, err := t.l.d.Store.CloseoutsOf(ctx, issue, "worker")
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, j := range jobs {
		if j.Branch == branch {
			ids = append(ids, j.ID)
		}
	}
	return ids, nil
}

// protected returns the sorted patterns of merge.protected_paths that the
// changed paths hit; "(malformed path)" stands for a path that fails closed.
func (t *tick) protected(files []string) []string {
	set := map[string]bool{}
	for _, f := range files {
		if p, ok := t.cfg.ProtectedBy(f); ok {
			if p == "" {
				p = "(malformed path)"
			}
			set[p] = true
		}
	}
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (t *tick) hasApproval(ctx context.Context, pr github.PullRequest, head, base string) (bool, error) {
	d := t.l.d
	reviews, err := d.Reader.ListReviews(ctx, pr.Number)
	if err != nil {
		return false, err
	}
	return approved(ctx, reviews, approvalCheck{
		self:       t.self,
		permission: d.Reader.GetPermission,
		covers: func(ctx context.Context, at string) (bool, error) {
			// An approval given at a commit the clone does not have (a
			// force-pushed tip) cannot be compared: it is not covered.
			ok, _, err := prgate.Covers(ctx, d.Git, d.RepoDir, at, head, base)
			if err != nil {
				t.log.Debug("merge.approval_not_comparable", "pr", pr.Number, "at", at, "error", err)
				return false, nil
			}
			return ok, nil
		},
	})
}

// needApproval labels the PR and posts one comment per head.
func (t *tick) needApproval(ctx context.Context, pr github.PullRequest, head string, patterns []string) {
	d := t.l.d
	t.log.Info("merge.needs_approval", "pr", pr.Number, "head", head, "patterns", strings.Join(patterns, ","))
	var list []string
	for _, p := range patterns {
		list = append(list, "`"+strings.ReplaceAll(p, "`", "'")+"`")
	}
	text := "This PR touches protected paths (" + strings.Join(list, ", ") + "). A person with write access must approve this head with a GitHub review; an approval counts only for the code it was given on, so a later push needs a new one."
	if err := agentjob.PostOnce(ctx, d.Reader, d.Effects, pr.Number, "<!-- ard:merger approval head="+head+" -->", text); err != nil {
		t.fail(pr.Number, "approval_comment", err)
		return
	}
	if !prgate.HasLabel(pr.Labels, labels.NeedsApproval) {
		if err := d.Effects.AddLabels(ctx, pr.Number, labels.NeedsApproval); err != nil {
			t.fail(pr.Number, "approval_label", err)
		}
	}
}

// needsHuman posts one fixed comment per (reason, head) and labels the PR. It
// never merges, so it returns false. detail is rendered after the text and is
// built from check names only.
func (t *tick) needsHuman(ctx context.Context, pr github.PullRequest, reason, detail string) bool {
	d := t.l.d
	text := map[string]string{
		"conflict":       "The branch conflicts with `main`. Resolve the conflict (or wait for review fix rounds, #41), then remove `ard:needs-human`.",
		"checks_failed":  "Required checks failed on the current head" + detail + ". Fix them and push, or re-run the checks, then remove `ard:needs-human`.",
		"no_closing_ref": "The PR body does not start with `Closes #<issue>`, so merging would not close the issue. Restore that first line, then remove `ard:needs-human`.",
		"no_worker_job":  "No worker job of this issue is recorded for this branch, so the merge commit would have no `Ard-Job` trailer. A person must merge this PR; then remove `ard:needs-human`.",
		"bad_message":    "The squash commit message cannot be built from this PR's title. Fix the title, then remove `ard:needs-human`.",
	}[reason]
	t.log.Info("merge.needs_human", "pr", pr.Number, "reason", reason, "head", pr.HeadSHA)
	marker := fmt.Sprintf("<!-- ard:merger needs-human reason=%s head=%s -->", reason, pr.HeadSHA)
	if err := agentjob.PostOnce(ctx, d.Reader, d.Effects, pr.Number, marker, text); err != nil {
		t.fail(pr.Number, "needs_human_comment", err)
		return false
	}
	if err := d.Effects.AddLabels(ctx, pr.Number, labels.NeedsHuman); err != nil {
		t.fail(pr.Number, "needs_human_label", err)
	}
	return false
}

func checkList(names []string) string {
	if len(names) == 0 {
		return ""
	}
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "`" + strings.ReplaceAll(n, "`", "'") + "`"
	}
	return ": " + strings.Join(q, ", ")
}

// updateBranch merges main into the PR branch, at most once per tick. The
// checks run again next tick on the new head.
func (t *tick) updateBranch(ctx context.Context, pr github.PullRequest, issue int) {
	d := t.l.d
	if t.updated {
		t.log.Info("merge.waiting_update", "pr", pr.Number, "reason", "another branch was updated this tick")
		return
	}
	t.updated = true
	err := d.Effects.UpdateBranch(ctx, pr.Number, pr.HeadSHA)
	switch {
	case err == nil && t.dry:
		t.log.Info("merge.would_update", "pr", pr.Number, "issue", issue, "head", pr.HeadSHA, "main", t.mainTip)
	case err == nil:
		t.log.Info("merge.update_branch", "pr", pr.Number, "issue", issue, "head", pr.HeadSHA, "main", t.mainTip)
	case errors.Is(err, github.ErrConflict):
		// A moved head is a 409 too: only a computed conflict on an
		// unchanged head is one.
		cur, rerr := d.Reader.GetPR(ctx, pr.Number)
		if rerr != nil {
			t.fail(pr.Number, "update_branch_reread", rerr)
			return
		}
		if cur.HeadSHA == pr.HeadSHA && cur.Mergeable != nil && (!*cur.Mergeable || cur.MergeableState == "dirty") {
			t.needsHuman(ctx, pr, "conflict", "")
			return
		}
		t.log.Info("merge.update_deferred", "pr", pr.Number, "head", cur.HeadSHA)
	case errors.Is(err, github.ErrNotMergeable):
		t.log.Info("merge.update_deferred", "pr", pr.Number, "reason", "not mergeable yet")
	default:
		t.fail(pr.Number, "update_branch", err)
	}
}

// merge squash-merges the PR with the head guard and ends the tick unless
// dry-run.
func (t *tick) merge(ctx context.Context, pr github.PullRequest, issue int, head string, jobIDs []string) bool {
	d := t.l.d
	n := pr.Number
	title, msg, err := committer.SquashMessage(pr.Title, n, jobIDs)
	if err != nil {
		t.log.Warn("merge.bad_message", "pr", n, "error", err)
		return t.needsHuman(ctx, pr, "bad_message", "")
	}
	res, err := d.Effects.MergePR(ctx, n, github.MergeInput{SHA: head, Title: title, Message: msg})
	switch {
	case errors.Is(err, github.ErrConflict):
		t.log.Info("merge.head_moved_at_merge", "pr", n, "head", head)
		return false
	case errors.Is(err, github.ErrNotMergeable):
		t.log.Warn("merge.not_mergeable", "pr", n, "head", head, "hint", "GitHub may still be computing; retrying next tick")
		return false
	case err != nil:
		t.fail(n, "merge", err)
		return false
	}
	if t.dry {
		t.log.Info("merge.would_merge", "pr", n, "issue", issue, "head", head, "title", title)
	} else {
		t.log.Info("merge.merged", "pr", n, "issue", issue, "head", head, "merge_sha", res.SHA)
		data, _ := json.Marshal(map[string]any{"pr": n, "head": head, "merge_sha": res.SHA})
		if err := d.Store.AppendEvent(ctx, state.Event{Kind: state.EventMerge, Issue: issue, Data: data}); err != nil {
			t.log.Warn("merge.event_failed", "pr", n, "error", err)
		}
	}
	if err := d.Effects.DeleteBranch(ctx, pr.HeadRef); err != nil {
		t.log.Warn("merge.delete_branch_failed", "pr", n, "branch", pr.HeadRef, "error", err)
	}
	return !t.dry
}
