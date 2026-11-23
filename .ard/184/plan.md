# Plan for #184

## Implementation

### Steps
1. `internal/closeout/closeout.go`: add to `Deps` an optional `PR func(ctx context.Context, n int) (github.PullRequest, error)` (new; nil means no check). Reason: the closer's `Comments` interface cannot read PR state. Wire it in `internal/servecmd/reaping.go` (`PR: w.Reader.GetPR`) and, if the reaper harness builds Deps, in `internal/reaper/harness_test.go` only when a test needs it.
2. `internal/closeout/plan.go`: add `stalePRPlan(p Plan, j Job, why string) Plan` (new). It keeps `Status`, `Summary`, `Review`, `Tail` fields, sets `Outcome = OutcomeStale`, `Reason = "pr gone: " + why`, clears `Files`, `LogPath`, `Terminal`, `Requests` and `Deferred`, and sets `Remove = []Label{running label of j}` only. Reason: the request label stays off the PR and no `ard:executor:failed` is set. Update the `OutcomeStale` doc comment to cover this case.
3. `internal/closeout/closeout.go`, `(*Closer).commit`: before committing, for `rec.Job.Stage == StageReview`, `rec.Plan.Review != nil`, `rec.Job.PR > 0` and `c.d.PR != nil`, call `c.d.PR`. If the PR is merged or `State != github.StateOpen`, replace the plan with `stalePRPlan` (why = `merged` or `closed`) and return without committing. If `GetPR` errors, log a warning and carry on to the commit. Then, in the existing commit-error path, when `errors.Is(err, committer.ErrBranchGone)` and the job is a review with `Plan.Review != nil`, apply `stalePRPlan` (why = `branch gone`) instead of `failedPlan`. Log `review.pr_gone` (Warn, fields `job`, `pr`, `issue`, `role`, `state` = merged|closed|branch_gone) in both cases. Other commit errors still fail as today. The plan is saved in the record after `commit`, so a resume replays the stale plan.
4. `internal/closeout/closeout.go`, `postReview`: when `rec.Plan.Reason` has the `pr gone` prefix, a failing `CreateReview`/`CreateComment` is logged at Warn and swallowed (leave `rec.ReviewMode` empty) so close-out still reaches `finish`. Posting stays best-effort and is still tried. Reason: a review on a merged PR is useful but must not fail the job.
5. `internal/closeout/review.go`: in `RenderReview` add a note when the plan's reason is `pr gone` ("The pull request was merged or closed before this review finished, so nothing was committed and the job is recorded as stale."). Simplest carrier is a new `ReviewPlan.PRGone string` field set by `stalePRPlan`.
6. Worker path: no code change. A worker's PR is a draft, and the merger only lands a PR after reviews pass with no worker running, so merge mid-job cannot occur. A human close or branch deletion mid-job still takes the existing `worker.push_failed` branch-gone path (`internal/worker/run.go:568`), which is a real, rare failure. Record this in the PR description and the docs edit.
7. Gate 2 reporting: no code change. `finish` calls `Store.Complete` for `OutcomeStale`, so the job is not `status = 'failed'` and the SQL in `docs/runbooks/gate-2.md` already excludes it. `internal/costs/rows.go` already prints `stale`. Add one sentence to `docs/runbooks/gate-2.md` below the failure-rate query saying a `stale` review (PR merged, closed or branch deleted) is not a failure.
8. `docs/proposal.md`, Reviewer loop item 5 (Close-out) or a new item 7: describe that if the PR was merged or closed, or its branch is gone, at close-out the job ends `stale`: no commit to the branch, no `ard:executor:failed`, no failure comment, `review.pr_gone` is logged with the state, the PR review is still posted best-effort, and Gate 2 does not count it. Also correct the "Close-out is resumable" paragraph's outcome list if it implies otherwise.

### Test plan
Command: `go test ./internal/closeout/... ./internal/reaper/... ./internal/servecmd/...` then `make test`.
All in `internal/closeout/review_test.go` using `newEnv`/`reviewJob` (set `e.d.PR = e.gh.GetPR`):
- `TestReviewPRMergedBranchDeleted`: seed PR 9 as `State: closed, Merged: true`, `e.cm.err = committer.ErrBranchGone`, approve verdict and `HeadMoved=false`. Expect `OutcomeStale`, nil error, zero commit calls, labels have no `ard:executor:failed` and no `:running`, no failure comment, one PR review posted, job state completed (not failed).
- `TestReviewPRClosedUnmerged`: PR closed, `Merged: false`, branch still exists. Expect the same, and no commit call.
- `TestReviewBranchGoneOnOpenPR`: PR open, committer returns `ErrBranchGone`. Expect stale (the issue's exact log case).
- `TestReviewPRGoneChangesVerdict`: a `changes` verdict on a merged PR gives stale and no `:changes` label.
- `TestReviewPRGonePostFails`: the review post errors; close-out still ends stale and the job closes.
- `TestReviewCommitOtherErrorStillFails`: a non-gone commit error on an open PR still yields `OutcomeFailed` (regression).
- `TestReviewGetPRErrorFallsThrough`: `GetPR` errors, the commit proceeds normally.
- `TestWorkerBranchGoneUnchanged`: existing worker cases in `cause_test.go` still pass.
Edge: a resumed close-out replays the stale plan without recommitting.

### Risk
Low to medium. Close-out is shared by every role, so the new branch is gated on review stage and a non-nil review plan. The only public interface change is an optional `Deps` field. No protected paths from `merge.protected_paths` are touched (`docs/` and `internal/` only). Not security-sensitive, no migrations. A transient `GetPR` error is deliberately ignored, so a merged PR could still reach the old commit path and then the `ErrBranchGone` fallback.

### Size
M. Files: `internal/closeout/closeout.go`, `plan.go`, `review.go`, `review_test.go`, `internal/servecmd/reaping.go`, `docs/proposal.md`, `docs/runbooks/gate-2.md`.
