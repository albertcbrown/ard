---
role: engineer
status: done
attempt: 1
---
Makes scanner finding-decode errors content-free: they now carry a line number and a fixed reason class, never scanner-supplied text, so secret-shaped values cannot reach the controller log.

## Changes
- internal/scanner/decode.go: encoding/json errors are classified (invalid JSON, wrong value type, unknown key, malformed) instead of wrapped with their own text.
- internal/scanner/finding.go: fingerprint, severity and file errors and the duplicate-fingerprint error no longer quote the value.
- internal/scanner/finding_test.go: updated expected substrings; added TestDecodeErrorsNeverEchoScannerText (table plus multi-line case).

## Testing
`go test ./internal/scanner/` passes. The new test fails if the marker appears in any error, and checks reason text and line numbers.

## Open issues
- Invalid UTF-8 bytes in JSON are replaced by encoding/json before validation, so that case has no test.
- Only the scanner package was tested, not `make test`. Overlaps with #215 around scan-failed logging; rebase if it lands first.
