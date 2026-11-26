---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/redact/scan_test.go"
    line: 121
    severity: low
    blocking: false
    message: |
      The "too long example plus real" case is special-cased by name inside the loop, and its `col` field is 0 and never checked.
      Give the table a `skipCol` flag, or set `want: 1, col: 29` and drop the branch, so the case also pins the position of the real key.
---
The change matches plan.md. The exemption is per match, limited to 20-character ids with the example suffix, and only the aws-access-key rule uses it. The tests cover mixed lines in both orders, a longer token, and the lowercase suffix. Approved.

## Checks
- Lint: `make lint` runs go vet and a gofmt check. gofmt is clean. go vet prints a parse error for `internal/roles/testdata/docs/skips/src/_x.go`, a file this change does not touch.
- Tests: `go test -cover -run 'Scanner|Redact' ./internal/redact/ ./internal/servecmd/` passes. `TestOnlyAddedLinesAreScanned` passes in `internal/worker`.
- Diff coverage: no profile was written. The new `awsExample` function and the `skip` branch in `scanLine` are exercised by `TestScannerAWSExample`, through both the skipped and the emitted paths.
- Not checked: gitleaks behaviour, which the plan leaves alone.
