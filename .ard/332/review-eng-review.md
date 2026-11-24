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
      plan.md named a "merge with a non-reviewed first parent" case. firstUpdateMerge returns "" for a non-two-parent commit or an unrelated first parent, and no test makes that path decide the result (the fake always returns {"rev","main"}). Add a case with an octopus or unrelated-first-parent head that must leave the path broken.
---
The merger tests the previous round asked for now exist: the review gate, the hand-edited merge that still re-requests, and the approval check, each asserting the `merge.cover_patch_equal` log. The core rule fails closed and follows plan.md, so this is approved. One non-blocking test gap remains.

## Checks
- Lint: `go vet` on the changed packages is clean, and `gofmt -l internal` flags only the pre-existing `internal/roles/testdata/docs/skips/src/_x.go`, which the change did not touch. I did not re-run `make lint`.
- Tests: `go test -cover ./internal/prgate ./internal/gitx ./internal/merger` passes, at 89.3%, 82.5% and 83.5%.
- `make docs-check` was not run (mkdocs is not installed).

## Previous findings
- internal/merger/merge.go (merger wiring untested): resolved. `TestPatchEqualSharedFileKeepsReviewCovered` covers the review gate, a hand-edited merge that still re-requests, and the approval check.
- internal/prgate/prgate.go (non-reviewed first parent and maxChain untested): still present, as a medium non-blocking finding at its current line.
- internal/prgate/prgate.go (soundness note, info): no longer applicable. It needed no action.
