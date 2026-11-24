---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/prgate/prgate.go"
    line: 300
    severity: medium
    blocking: false
    message: |
      firstUpdateMerge still has no test for a two-parent head whose first-parent chain misses the reviewed commit, nor for the maxChain cap.
      The fake Parents in TestCoversPatchGitError always returns {"rev","main"}, and the real-repo tests only cover a non-merge head. Add a case where head is a merge whose first parent chain never reaches reviewed; the path must stay broken.
---
The patch-id cover rule matches the issue and plan, fails closed on git errors and on changed patches, and the merger wiring and docs are updated. Approved; the one earlier non-blocking test gap is still open.

## Checks
- Lint: no repository lint entry point run; `go vet ./internal/prgate ./internal/gitx ./internal/merger` is clean.
- Tests: `go test ./internal/prgate ./internal/gitx ./internal/merger` passes.
- Diff coverage: not measured.

## Previous findings
- internal/prgate/prgate.go (unrelated first parent and maxChain untested): still present, medium and non-blocking.
