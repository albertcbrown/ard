---
role: engineer
status: done
attempt: 1
---
Conflict rounds, part B: when a PR conflicts with main, the worker resolves the merge and the committer pushes a merge commit with main's tip as second parent, under a tree invariant and lease push. This round adds the missing test for the conflict-round diff guard.

## Changes
- Committer and gitx: merge-parent commit, tree invariant, lease push (unchanged this round).
- Worker conflict flow: `conflict.md` input, trial merge, marker check, diff facts measured against main's tip (unchanged this round).
- `internal/worker/conflict_round_test.go`: new `TestConflictRoundDiffGuardOwnChanges`.

## Testing
Ran `go test ./internal/worker -run TestConflict`: passes. The new test sets diff_max_lines to 20 and has main add 100 lines. Case 1: the resolution passes and main's file is not declared. Case 2: the PR adds 100 lines of its own and is held with a line-limit hold and no commit request.

## Review findings
- internal/worker/run.go:626 (blocking, diff guard untested): fixed, both cases added.
- internal/worker/run.go:720 (workflow rejection, other untested items): not changed: non-blocking, left for a follow-up.
- internal/committer/merge.go:25 (runMerge coverage): not changed: non-blocking, left for a follow-up.
- internal/servecmd/serve.go:739 (Rounds wiring, end-to-end test): not changed: non-blocking, left for a follow-up.
- internal/reviewfix/loop.go:403 (hard-coded main, weaker second-parent check): not changed: non-blocking, low severity.

## Open issues
- The non-blocking test gaps above remain: workflow-rejection hold, MaxFiles hold, overlap needs-human, committer stale-lease and undeclared-diff cases, Rounds wiring, end-to-end.
