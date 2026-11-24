---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/prgate/prgate.go"
    line: 313
    severity: medium
    blocking: false
    message: |
      The merge-with-unrelated-first-parent case and the maxChain cap in firstUpdateMerge are still untested.
      A plain non-merge head is covered (the "PR changes again" step), but plan.md also names a merge whose first parent is not the reviewed commit. Add a case where the head is a two-parent merge whose first-parent chain misses reviewed; the path must stay broken.
---
The patch-id cover rule matches plan.md and the issue, fails closed on git errors and on a changed patch, and the merger wiring is tested. Approved; one non-blocking test gap remains.

## Checks
- Lint: no `make lint` run; `go vet` on `internal/prgate`, `internal/gitx` and `internal/merger` is clean.
- Tests: `go test ./internal/prgate ./internal/gitx ./internal/merger` passes.
- Diff coverage: not measured.

## Previous findings
- internal/prgate/prgate.go (unrelated first parent and maxChain untested): still present, medium and non-blocking.
