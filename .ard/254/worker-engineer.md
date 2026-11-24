---
role: engineer
status: done
attempt: 1
---
Narrows the log secret scan's generic-assignment rule so unquoted Go selectors/identifiers and all-digit token counts are no longer flagged, while quoted and opaque values still are.

## Changes
- internal/redact/rules.go: the regex now captures the optional opening quote; added codeRef (selector chain with segments of at most 24 bytes, or a bare identifier under 32 bytes) and numeric helpers; updated the rule comment.
- internal/redact/scan.go: scanLine uses the renumbered groups and skips unquoted codeRef/numeric values.
- internal/redact/scan_test.go: table tests for the false positives (selectors, token counts) and true positives (unquoted opaque, quoted, quoted dotted, unquoted dotted opaque), plus a scanString helper.

## Testing
`go vet` and `go test` in internal/redact pass, including the existing per-rule, negatives and seam tests. gofmt is clean. A run of `go test ./...` for the whole repo timed out here, so I did not get a full-suite result.

## Open issues
- The whole-repo test run did not finish in the sandbox time limit; only internal/redact was verified.
- An unquoted identifier-shaped value under 32 bytes (letters, digits and underscores, no dot) is now skipped by design, per the plan.
