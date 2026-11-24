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
	"github.com/albertcbrown/ard/internal/closeout"
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

	// Review labels of roles that are not configured (a stale or hand-made
	// :changes or :running) still block.
	for _, l := range pr.Labels {
		if p, ok := labels.Parse(l); ok && p.Stage == labels.StageReview && (p.State == labels.StateChanges || p.State == labels.StateRunning) {
			t.skip(n, prgate.ReasonChanges)
			return false
		}
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
	bases, err := d.Git.MergeBaseAll(ctx, d.RepoDir, t.mainTip, head)
	if err != nil {
		t.fail(n, "merge_base", err)
		return false
	}
	if len(bases) != 1 {
		// A criss-cross history has several bases and the choice changes what
		// counts as the PR's own change: a human decides.
		t.log.Warn("merge.ambiguous_base", "pr", n, "bases", len(bases))
		return false
	}
	base := bases[0]

	// e. Every requested review passed (by its close-out record, not the label)
	// at a head that still covers this one.
	for _, role := range v.Roles {
		reviewed, passed, err := closeout.ReviewPassed(ctx, d.Store, issue, n, role)
		if err != nil {
			t.fail(n, "review_record", err)
			return false
		}
		if !passed {
			t.log.Info("merge.reviews_stale", "pr", n, "issue", issue, "role", role, "reason", "no passing close-out record",
				"hint", "the review has not passed on this PR; it re-runs when the role label is requested")
			return false
		}
		cov, err := prgate.CoversDetail(ctx, d.Git, d.RepoDir, reviewed, head, base, issue)
		if err != nil {
			t.fail(n, "review_cover", err)
			return false
		}
		broken := cov.Broken
		if len(cov.PatchEqual) > 0 {
			t.log.Info("merge.cover_patch_equal", "pr", n, "issue", issue, "role", role, "reviewed", reviewed, "head", head, "paths", strings.Join(cov.PatchEqual, ","))
		}
		if !cov.OK {
			t.log.Info("merge.reviews_stale", "pr", n, "issue", issue, "role", role, "reviewed", reviewed, "head", head, "paths", len(broken),
				"hint", "the merger re-requests this review once per head")
			t.rerequestReview(ctx, pr, issue, role, reviewed, head)
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
	statuses, err := d.Reader.ListCommitStatuses(ctx, head)
	if err != nil {
		t.fail(n, "commit_statuses", err)
		return false
	}
	if err := t.resolveApps(ctx, statuses); err != nil {
		t.fail(n, "app_id", err)
		return false
	}
	switch st, names := prgate.EvalChecks(t.required, runs, statuses, graceOver); st {
	case prgate.ChecksPending:
		t.log.Info("merge.waiting_checks", "pr", n, "head", head, "checks", strings.Join(names, ","), "grace_over", graceOver)
		return false
	case prgate.ChecksRed:
		return t.needsHuman(ctx, pr, "checks_failed", checkList(names))
	}

	// j. Protected paths need a write-access approval of this head.
	files, err := d.Git.CodeChanged(ctx, d.RepoDir, base, head)
	if err != nil {
		t.fail(n, "changed_paths", err)
		return false
	}
	if patterns := t.protected(files); len(patterns) > 0 {
		ok, err := t.hasApproval(ctx, pr, head, base, issue)
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

func (t *tick) hasApproval(ctx context.Context, pr github.PullRequest, head, base string, issue int) (bool, error) {
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
			cov, err := prgate.CoversDetail(ctx, d.Git, d.RepoDir, at, head, base, issue)
			if err != nil {
				t.log.Debug("merge.approval_not_comparable", "pr", pr.Number, "at", at, "error", err)
				return false, nil
			}
			if len(cov.PatchEqual) > 0 {
				t.log.Info("merge.cover_patch_equal", "pr", pr.Number, "issue", issue, "at", at, "head", head, "paths", strings.Join(cov.PatchEqual, ","))
			}
			return cov.OK, nil
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

// rerequestReview removes role's :done label so the reviewer claims the role
// again at the current head. It acts once per head: a review_rerequest event
// records it. In dry-run only the ledger is written, so the event is not.
func (t *tick) rerequestReview(ctx context.Context, pr github.PullRequest, issue int, role, reviewed, head string) {
	d := t.l.d
	n := pr.Number
	done := labels.Done(labels.StageReview, role)
	if !prgate.HasLabel(pr.Labels, done) {
		return
	}
	evs, err := d.Store.Events(ctx, state.EventFilter{Kind: state.EventReviewRerequest, Issue: issue, Stage: "review", Role: role})
	if err != nil {
		t.fail(n, "rerequest_events", err)
		return
	}
	for _, e := range evs {
		var data struct {
			PR   int    `json:"pr"`
			Head string `json:"head"`
		}
		if json.Unmarshal(e.Data, &data) == nil && data.PR == n && data.Head == head {
			return
		}
	}
	if err := d.Effects.RemoveLabel(ctx, n, done); err != nil {
		t.fail(n, "rerequest_label", err)
		return
	}
	if t.dry {
		t.log.Info("merge.would_rerequest", "pr", n, "issue", issue, "role", role, "reviewed", reviewed, "head", head)
		return
	}
	t.log.Info("merge.review_rerequested", "pr", n, "issue", issue, "role", role, "reviewed", reviewed, "head", head)
	data, _ := json.Marshal(map[string]any{"pr": n, "head": head, "reviewed": reviewed})
	if err := d.Store.AppendEvent(ctx, state.Event{Kind: state.EventReviewRerequest, Issue: issue, Stage: "review", Role: role, Data: data}); err != nil {
		t.log.Warn("merge.event_failed", "pr", n, "error", err)
	}
}

// resolveApps fills AppID on the statuses a pinned required rule could use: a
// status posted by "<slug>[bot]" gets the App's id (cached per process). A
// status from a user stays 0 and so never satisfies a pinned rule. A lookup
// error is returned, and the caller fails closed.
func (t *tick) resolveApps(ctx context.Context, statuses []github.CommitStatus) error {
	pinned := map[string]bool{}
	for _, rc := range t.required {
		if rc.IntegrationID != 0 {
			pinned[rc.Context] = true
		}
	}
	if len(pinned) == 0 {
		return nil
	}
	l := t.l
	for i := range statuses {
		s := &statuses[i]
		slug, ok := strings.CutSuffix(s.Creator, "[bot]")
		if !pinned[s.Context] || !ok || slug == "" {
			continue
		}
		l.mu.Lock()
		id, hit := l.apps[slug]
		l.mu.Unlock()
		if !hit {
			var err error
			if id, err = l.d.Reader.AppID(ctx, slug); err != nil {
				return fmt.Errorf("resolve app %s: %w", slug, err)
			}
			l.mu.Lock()
			l.apps[slug] = id
			l.mu.Unlock()
		}
		s.AppID = id
	}
	return nil
}
