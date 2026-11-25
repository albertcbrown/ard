---
role: engineer
status: done
attempt: 1
---
Serialises fetches with worktree add/remove on the controller's clone. The clone lock was exclusive and held only by worktree admin, so a fetch could read a half-added or half-removed worktree entry and abort. This was a production race, not only a test flake.

## Changes
- `internal/wtlock/wtlock.go`: the lock is now shared/exclusive. `Lock` stays exclusive with the same signature. New `RLock` is the shared side. A writer gate keeps writers from being starved, and every wait honours ctx.
- `internal/gitx/local.go`: `fetch` takes the shared lock (a not-yet-existing repo fetches unlocked). `commonDir` is extracted. `WorktreeAdd` and `WorktreeRemove` start their 30 s timeout after the lock is acquired, so a long fetch cannot make them time out. Remove waits at most 10 min for the lock.
- `docs/proposal.md`: states the concurrency rule in the local-executor and committer paragraphs.
- Tests: new `RLock` cases in `internal/wtlock/wtlock_test.go`. `internal/gitx/gitx_test.go` gains `TestFetchSerialisedWithWorktreeAdmin`, which holds the exclusive lock and asserts a fetch waits, and `TestFetchSharedWithFetch`.

## Testing
- `go test ./internal/wtlock ./internal/gitx`: pass.
- `go test -count=10 -race ./internal/committer -run TestBranchesStress`: pass. I did not run the full `-count=200`, nor under CPU load.
- `make lint test`: all packages pass. `go vet` prints a parse note for `internal/roles/testdata/.../_x.go`, which is not caused by this change and does not fail the run.

## Open issues
- No retry on the commondir error was added, as the plan says.
- The repeated race run stays a local check and is not added to CI.
