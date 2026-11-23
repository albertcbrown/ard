---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/scanner/finding_test.go"
    line: 144
    severity: medium
    blocking: false
    message: |
      plan.md's test plan lists a "bad UTF-8 in a field" case that TestDecodeErrorsNeverEchoScannerText does not include.
      encoding/json replaces invalid UTF-8 in strings with U+FFFD, so such input never reaches the UTF-8 check and the case is largely covered by the control-character cases. Add it anyway, or note why it was dropped.
---
The change replaces every scanner-value echo in decode errors with fixed reason classes, as plan.md and the issue ask. The new table test checks that the marker never appears, the reason text, and the line numbers, and it passes. One small test-plan gap is non-blocking.

## Checks
- Lint: `make lint` runs `go vet ./...`, which stops on `internal/roles/testdata/docs/skips/src/_x.go` (a parse error in a testdata file the change did not touch). Nothing was reported for the changed files.
- Tests: `go test ./internal/scanner/` passes.
- Diff coverage: not run with a profile. Every added branch in `classifyJSONError` and the changed messages is exercised by the table tests, apart from the generic "malformed" fallback.
