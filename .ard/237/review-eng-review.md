---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/budget/notice.go"
    line: 125
    severity: medium
    blocking: false
    message: |
      If AppendEvent fails after the comment is posted, the marker is missing and the next tick (one minute later) posts the notice again, every minute until the write succeeds.
      The failure is only logged. Remember the posted key in memory (a map on Loop, guarded by mu) so a failed write cannot cause repeats within a process, and add a test with a failing AppendEvent.
---
The change matches the issue and plan: one notice per capped scope and UTC day, deduplicated by a `budget_capped` event, a comment with no agent text and no `@`, the loop registered, and the docs updated. Approved. The one non-blocking gap from the last round is still present.

## Checks
- Lint: not re-run. The last round's `make lint` stopped on a parse error in an untouched testdata file and reported nothing on the changed files.
- Tests: `go test -cover ./internal/budget` passes (87.6% statement coverage). I did not run a per-line diff coverage report.

## Previous findings
- internal/budget/notice.go (repeat notice after a failed AppendEvent): still present, now at line 125. It stays non-blocking.
