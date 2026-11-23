---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/scanner/marker.go"
    line: 168
    severity: low
    message: |
      neutralizeBody treats only ``` fences and inline backtick spans as code. Indented code blocks and ~~~ fences are not recognised, so mentions inside them are defused too (harmless, but they render with a fullwidth at-sign). Safe direction; no action needed.
---
The change neutralises titles centrally (SafeTitle, IssueTitle, lineText), defuses mentions and tags in bodies, and hardens the cve, error-codes and docs scanners. It adds hostile-input tests for each and updates the proposal as the issue asks. Approved.

## Checks
- Lint: `make lint` ran. Its tail showed only module downloads and a parse message for the existing testdata file `internal/roles/testdata/docs/skips/src/_x.go`, which this change did not touch. No findings on changed lines.
- Tests: `go test ./internal/scanner ./internal/roles` passes.
- Diff coverage: not measured; the new SafeTitle, InlineCode and neutralizeBody code has direct tests in marker_test.go, and each scanner has a hostile-path test.
