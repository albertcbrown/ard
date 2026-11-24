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
      The "first parent is not the reviewed commit" skip and the maxChain cap are still untested.
      firstUpdateMerge returns "" for a non-two-parent commit or an unrelated first parent, and no test makes that path decide the result (errPatchGit always returns {"rev","main"}). Add a case with an octopus or unrelated-first-parent head that must leave the path broken.
---
The patch-id cover rule fails closed on git errors and on a changed patch, matches plan.md and the issue, and the merger wiring is tested. Approved. One non-blocking test gap from the previous round remains.

## Checks
- Lint: `go vet` on the changed packages is clean. I did not run `make lint`.
- Tests: `go test ./internal/prgate ./internal/gitx ./internal/merger` passes.
- Diff coverage: I did not re-measure it. The only added code I found no test for is the early return in `firstUpdateMerge` (non-merge head or unrelated first parent) and the `maxChain` cap.

## Previous findings
- internal/prgate/prgate.go (non-reviewed first parent and maxChain untested): still present, as a medium non-blocking finding.
