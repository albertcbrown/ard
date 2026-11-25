---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/gitx/local.go"
    line: 57
    severity: low
    message: |
      fetchLock treats any `rev-parse --git-common-dir` failure (other than ctx cancellation) as "not a repository yet" and fetches unlocked.
      A transient rev-parse failure on a real clone would silently skip the lock. Acceptable per plan.md, but consider limiting the unlocked fallback to the case where the directory has no git data.
  - file: "internal/wtlock/wtlock_test.go"
    line: 92
    severity: low
    message: |
      plan.md named TestWriterCancelReleases and TestRUnlockIdempotent as separate cases. They are covered inside TestRLockBlocksLock (a writer cancelled holding 63 slots, then Lock succeeds) and TestRLockShared (double unlock), so this is only a naming difference.
---
The change makes fetches take a shared lock and worktree add/remove take it exclusively. This matches plan.md, fixes the race the issue describes, and the new tests cover both lock sides and the gitx wiring. Approved.

## Checks
- Lint: `make lint` reports only `internal/roles/testdata/docs/skips/src/_x.go:3:8: expected ')', found 'EOF'`. That is a testdata file the change did not touch.
- Tests: `go test -race -count=3 ./internal/wtlock ./internal/gitx` and `go test -race -count=30 ./internal/committer -run TestBranchesStress` pass.
- Diff coverage: not measured with a profile. The new wtlock and gitx functions are all exercised by the added tests.
- Deadlock review: the lock is held only around git commands, and no path holds the exclusive side while fetching.
