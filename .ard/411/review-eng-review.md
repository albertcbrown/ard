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
      A transient rev-parse failure on a real clone would skip the lock silently. Acceptable per plan.md, but consider limiting the unlocked fallback to the case where the directory has no git data.
---
Fetches take the shared side of the clone lock and worktree add/remove take the exclusive side, which fixes the race in the issue. The lock logic is sound and the tests cover both sides and the gitx wiring. Approved.

## Checks
- Lint: not re-run this round. The code looks unchanged from the previous round, where `make lint` reported only a pre-existing testdata error in a file this change did not touch.
- Tests: `go test -race -count=1 ./internal/wtlock ./internal/gitx` passes. `go test -race -count=20 ./internal/committer -run TestBranchesStress` passes.
- Diff coverage: no coverage profile was made. The added functions are all called by the new tests.

## Previous findings
- internal/gitx/local.go (fetchLock fallback): still present, low and non-blocking.
