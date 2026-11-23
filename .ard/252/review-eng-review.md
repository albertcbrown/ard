---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/merger/merge.go"
    line: 365
    severity: low
    message: |
      If AppendEvent fails after the :done label was removed, only a warning is logged. This is safe today because the next tick no longer sees the label, but a short comment saying so would help the next reader.
---
The merger now removes a stale role's :done label once per head, records a review_rerequest event, and logs it. Main-only moves are not re-requested, and dry-run writes only to the ledger. This matches the issue and plan, and the new tests cover the cases the issue lists. Approved.

## Checks
- Lint: `make lint` printed mostly dependency-download output, and its only visible complaint was a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file this change does not touch. I could not confirm a clean run beyond that.
- Tests with coverage: `go test -cover ./internal/merger ./internal/state` passes, merger at 85.2%. The new tests execute `rerequestReview`: once per head, main-only move, dry-run, no done label, label error retry, and no passing record.
- I did not verify in the reviewer's claim code that removing :done alone makes the role re-run. `internal/reviewer/candidates.go` excludes roles that have a :done label, which suggests it does.
