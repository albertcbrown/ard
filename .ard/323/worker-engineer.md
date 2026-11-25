---
role: engineer
status: done
attempt: 1
---
Implements conflict rounds, part B: the worker resolves a merge with `main`, and the committer code lane pushes a two-parent merge commit built from main's tree plus the worker's declared files, enforcing the security review's single tree rule. Wires part A's switch in `servecmd`.

## Changes
- gitx: `MergeNoCommit` and `MergeAbort` (hardened: empty-tree attribute source, no rerere/renormalize/submodule/autocrlf/gpgsign/hooks, no -X); `CommitOp.Parents` is the full parent list, builds the commit with `commit-tree`, and skips the nothing-to-commit check.
- committer: `CodeRequest.MergeParent`, `ErrMergeParent`, and `runMerge` (new `merge.go`). It runs no `git merge`: it fetches main into a private ref and refuses unless it equals `MergeParent`, requires the branch still at `Base` (`ErrConflict`, no retry), applies the files over main's tree, checks that the diff against main is within the declared files and passes `CheckCodePath` and modes, commits with parents [branch tip, main tip], and pushes with a lease. `attempt` now calls a shared `apply`.
- worker: `conflict.go` (conflict round detection, own fetch of main as the only merge parent, scratch trial merge, capped and escaped `conflict.md`, advisory marker scan via `os.Root`); `run.go` feeds it instead of `review-feedback.md`, runs the merge before the first agent step, runs no git on the agent's worktree afterwards, measures the diff guard against the fetched main tip, passes `MergeParent`, and holds on a workflow-file push rejection or over `MaxFiles`.
- reviewfix: `mergeIncludes` also requires the second parent to be an ancestor of fetched main.
- servecmd: the review fix loop is built once and passed as the merger's `Rounds`.
- docs/proposal.md updated.

## Testing
`go test` for gitx, committer, effects, worker, reviewfix, merger and servecmd pass. New: gitx merge tests (conflict, clean, bad ref, attribute immunity, parents); committer tests (merge parents and tree, not main tip, invalid, protected or undeclared paths refused, stale branch with no retry, main moved, same tree as main, over MaxFiles); worker unit tests for the marker scan and name quoting. `TestTransientCancelledInBackoff` failed once in a full run (a known timing flake) and passed when rerun. `make lint` stops on gofmt of a testdata file (`internal/roles/testdata/docs/skips/src/_x.go`) that I did not touch.

## Open issues
- No end-to-end fake test of a conflict round, and no worker tests for `conflictInput`, the diff guard in conflict mode or the workflow-rejection hold. The planned `TestCommitCodeMergeNonLeasePush` and dry-run tests are also missing; the lane always pushes with a lease. Follow-up tests needed.
- `conflict.md` has the conflicted paths and change counts for each side, not hunks or commit summaries.
- The S6 check uses ancestor-of-main, not strictly first-parent history.
- The starter config has no switch to turn on (protected), and the worker prompt's conflict section is a separate human PR that should merge first.
- The workflow-rejection hold matches the word "workflow" in the push error.
- `CommitOp.Parents` is the full parent list (HEAD is ignored), not extra parents beyond HEAD as the plan said.
- A maintainer should review the committer changes closely.
