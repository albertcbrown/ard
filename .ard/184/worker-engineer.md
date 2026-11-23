---
role: engineer
status: done
attempt: 1
---
Review close-out now ends `stale` instead of `failed` when the PR was merged or closed, or its branch is gone. Nothing is committed, no `ard:executor:failed` or failure comment appears, and Gate 2 does not count it as an executor failure.

## Changes
- `internal/closeout/closeout.go`: new optional `Deps.PR`. Before committing, a review checks PR state; a merged or closed PR, or a committer `ErrBranchGone`, switches to the stale plan and logs `review.pr_gone` with the state. A failed PR read falls through to the commit. Posting the review on a gone PR is best-effort.
- `internal/closeout/plan.go`: `stalePRPlan` (only `:running` removed) and an updated `OutcomeStale` comment.
- `internal/closeout/review.go`: `ReviewPlan.PRGone` and a note in the rendered review.
- `internal/servecmd/reaping.go`: wires `Deps.PR` to the reader.
- `internal/closeout/prgone_test.go`: merged and deleted, closed unmerged, branch gone on an open PR, changes verdict, post failure, other commit errors still failing, PR read error.
- `docs/proposal.md` (reviewer item 7) and `docs/runbooks/gate-2.md`: describe the case.

## Testing
`go test ./...` passes, including the new tests. gofmt and go vet are clean on the changed packages.

## Open issues
- No worker-path change: a worker's draft PR cannot be merged while it runs, and a branch deleted mid-job stays a genuine failure.
- Gate 2 needed no code change: stale jobs complete rather than fail.
