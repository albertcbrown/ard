---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/budget/notice.go"
    line: 126
    severity: medium
    blocking: false
    message: |
      If AppendEvent fails after the comment is posted, the marker is missing and the next tick (one minute later) posts the notice again, every minute until the write succeeds.
      The failure is only logged. Remember the posted key in memory (a map on Loop, guarded by mu) so a failed write cannot cause repeats within a process, and add a test with a failing AppendEvent.
---
The change matches the issue: one notice per capped scope and UTC day, deduplicated through a `budget_capped` event that survives a restart, a comment with no agent text and no `@`, and the loop registered. Approved; one non-blocking robustness gap is noted.

## Checks
- Lint: `make lint` runs `go vet` and then stops on `internal/roles/testdata/docs/skips/src/_x.go` (parse error in a testdata file this change did not touch). Nothing was reported on the changed files.
- Tests: `go test -cover ./internal/budget ./internal/servecmd` passes (87.6% and 80.7% statement coverage). I did not run a per-line diff coverage report.
