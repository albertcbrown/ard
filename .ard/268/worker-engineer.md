---
role: engineer
status: done
attempt: 1
---
Turns off git's auto gc and auto maintenance for every git command the controller and local executor run. A detached background `worktree prune` outlives the per-repository worktree lock and is the leading suspect for the vanished worktree admin directory.

## Changes
- `internal/gitx/gitx.go`: `run` adds `-c gc.auto=0 -c maintenance.auto=false`, with a comment saying why.
- `internal/executor/local/git.go`: the same two options, because this helper does not go through gitx.
- `internal/gitx/gitx_test.go`: new `TestRunDisablesAutoGC`.

## Testing
- `go test -race ./internal/gitx ./internal/committer ./internal/executor/...` passes.
- `go test -race -count=50 -run TestBranchesStress ./internal/committer` passes.

## Open issues
- The failure did not reproduce locally, so the auto-gc cause is unconfirmed (no GIT_TRACE diagnosis was run).
- Skipped the plan's optional retry in `WorktreeAdd` and the concurrent auto-gc test, to keep the change small. Per-repository serialization already exists in `internal/wtlock`.
