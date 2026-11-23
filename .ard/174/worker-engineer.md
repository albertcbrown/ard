---
role: engineer
status: done
attempt: 1
---
Job cleanup now removes read-only trees such as Go's module cache, so job homes and worktrees are no longer leaked. Host job steps also get a Go flag so future module caches are writable.

## Changes
- internal/executor/local/local.go: `chmodWritable` is now a recursive pre-order walk that chmods each directory before reading it (handles 0555 and 0000 dirs) and never follows or chmods symlinks. New `removeAllWritable` helper; used in the Prepare failure path.
- internal/executor/local/proc_unix.go: `Cleanup` removes the worktree and the whole job home with `removeAllWritable` (previously only the sidecar subtree was made writable). The Reaper and worker step cleanup both go through `Executor.Cleanup` (confirmed at internal/worker/run.go:748).
- internal/executor/local/env.go: host env adds `GOFLAGS=-modcacherw`.
- internal/executor/local/cleanup_test.go (new): read-only tree (0555 dir, 0444 file), unreadable 0000 dir, symlink to an outside read-only tree (not followed, modes unchanged), missing path.
- internal/executor/local/local_test.go: `TestEnvScrubbed` expects GOFLAGS.
- docs/proposal.md: notes GOFLAGS, the per-job (unshared) module cache and the cleanup behaviour.

## Testing
`go test ./internal/...` passes. gofmt reports nothing. A first run failed in TestEnvScrubbed (new env var, test list updated) and TestReadOnlyHost (a chmod landed in the wrong function, fixed).

## Open issues
- The shared module cache is deliberately not implemented: a cache jobs can write to allows poisoning across jobs. Suggest a separate issue. Jobs still download modules per step.
- `make lint` was not run.
