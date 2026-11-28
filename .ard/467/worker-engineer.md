---
role: engineer
status: done
attempt: 1
---
When a conflict round's trial merge of main is clean, the controller now merges main into the branch itself, pushes it through the diff guard and code lane, and closes the round as done. No agent job is created and no tokens are spent, so reviewed code is no longer re-run as a first run.

## Changes
- internal/worker/conflict.go: a clean trial merge sets `clean`, keeps the scratch worktree (`wt`, `branchTip`, `release`) for the caller; other paths still clean up as before.
- internal/worker/cleanmerge.go (new): `cleanMergeRound` logs `worker.conflict_clean_merge`, runs the diff guard against the main tip, pushes with `MergeParent` set to the main tip, updates the PR and finishes with a controller-written summary (uses the existing no-agent `Reopened` close-out path).
- internal/worker/run.go: a clean round calls `cleanMergeRound` instead of `exec` and always releases the worktree.
- internal/worker/conflict_round_test.go: updated `TestConflictRoundCleanMerge`; added `TestConflictRoundCleanMergePushes` and `TestConflictRoundCleanMergePushFailures` (branch moved, transient).
- docs/proposal.md: one sentence in the conflict-round paragraph.

## Testing
`go test ./internal/worker/` passes; `go vet ./internal/worker/` clean. The new tests fail if an agent step starts, and check no executor job, one commit request with the main tip as `MergeParent` and the branch tip as base, the log event, and the push-failure causes.

## Open issues
- A clean merge where the branch has no changes of its own against main (the code lane rejects zero files) ends needs-human with a hold instead of pushing.
- The protected-path edge case is covered by the existing guard and committer; I added no dedicated test for it.
