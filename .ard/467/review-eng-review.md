---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/worker/cleanmerge.go"
    line: 94
    severity: medium
    blocking: false
    message: |
      The no-changes hold and the guard-hold branches of cleanMergeRound have no test.
      Add cases for a branch whose diff against main is empty (expects the hold text and no push) and for a guard reject (for example a protected path). plan.md's edge-case test for a protected path was not written.
  - file: "internal/worker/conflict_round_test.go"
    line: 214
    severity: medium
    blocking: false
    message: |
      The push test uses the fake code lane, so it never checks that the pushed commit is a merge whose second parent is the main tip, which the issue asked for. It only checks that MergeParent and Base are set on the request.
      The plan asked for a `rev-parse branch^2` assertion. Add it if the harness can run the real committer. Otherwise leave it, since the committer has its own tests.
  - file: "internal/worker/cleanmerge.go"
    line: 54
    severity: low
    blocking: false
    message: |
      The "branch moved" test does not hook the race between fetch and push as plan.md described. It injects ErrConflict directly. That is acceptable because the committer owns the lease, but it is a deviation from the plan.
---
The change matches the issue and plan.md. A clean trial merge now goes through the guard and the code lane with no agent job. The scratch worktree is released on every path, and the push-failure causes are tested. The gaps are untested hold paths, which are not blocking.

## Checks
- Lint: `make lint` stops with `internal/roles/testdata/docs/skips/src/_x.go:3:8: expected ')', found 'EOF'`. That file is test data the change does not touch, so I treated it as a failure that was there before. It reported nothing on the changed files.
- Tests: `go test -count=1 -cover ./internal/worker/` passes, with 81.6% statement coverage for the package.
- Diff coverage: I did not compute per-line coverage. The no-changes hold (cleanmerge.go:94) and the guard hold and error branches look unexecuted, which is the first finding.
