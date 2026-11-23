# Plan for #231

## Implementation

### Steps
1. `internal/scanner/decode.go`, `decodeStrict`: stop wrapping the `encoding/json` error with `%v`. Its text can quote scanner input (`unknown field "<key>"`, `invalid character 'x' ...`). Classify with `errors.As` and return fixed strings: `*json.SyntaxError` or `io.ErrUnexpectedEOF` -> `not a finding object: invalid JSON`; `*json.UnmarshalTypeError` -> `not a finding object: wrong value type` (do not use its Field/Value); a string starting `json: unknown field` (there is no typed error; use `strings.HasPrefix`) -> `not a finding object: unknown key`; anything else -> `not a finding object: malformed`. Keep the `trailing data after the finding object` message (already content-free).
2. `internal/scanner/finding.go`, `validateFinding`: drop the scanner value from three messages and use reason classes:
   - fingerprint (line 203): `fingerprint: invalid UTF-8 or control characters`
   - severity (line 211): `severity: not one of critical, high, medium, low, info`
   - the other messages already contain only constants and need no change.
3. `internal/scanner/finding.go`, `checkFilePath`: `file: not a valid path` and `file: not repo-relative (absolute, unclean or contains ..)`. Do not echo `p`.
4. `internal/scanner/finding.go`, `DecodeFindings` (line 126): `duplicate fingerprint (first on line %d)` without the fingerprint. The line number is enough.
5. `DecodeError.Line` already carries the line number; leave `DecodeErrors.Error()` as is. `pipeline.go:285` logs `se.Error()`, which is now safe, and needs no change. If #215 has changed that line, rebase onto it.
6. Update the existing `wantErr` substrings in `internal/scanner/finding_test.go` that the new wording breaks ("control", "without ..", "repo-relative", "clean", "not a finding" still holds).

### Test plan
Run: `go test ./internal/scanner/` (then `make test`).
- New table test `TestDecodeErrorsNeverEchoScannerText` in `internal/scanner/finding_test.go`. Each case is an input line containing a marker such as `sk-live-SECRET123`. Assert the error from `DecodeFindings` does not contain the marker, and that `DecodeErrors[0].Line` is right.
  - secret-shaped fingerprint with a control character (`"sk-live-SECRET123\u0000"`)
  - secret-shaped fingerprint duplicated on two lines
  - secret-shaped file: absolute path, `../` path, unclean path, and one with a control character
  - secret-shaped unknown key (`{"sk-live-SECRET123":1,...}`)
  - invalid severity set to the marker
  - wrong-typed value, e.g. `"title":"..."` replaced by a number, next to the marker
  - non-JSON line `sk-live-SECRET123`, to cover the syntax-error text
  - trailing data containing the marker
  - bad UTF-8 in a field
- Edge: multiple bad lines, to check every reported error stays clean and the line numbers are right. Also confirm the reason class text appears (e.g. `fingerprint:`, `file:`).
- Existing tests (`TestDecodeFindings`, `TestDecodeErrorsReportsUpToTwenty`) must still pass with their updated substrings.

### Risk
Low. The change is to error text inside `internal/scanner`, with no interface, migration or concurrency change. No protected path from `merge.protected_paths` is touched. It is security-motivated hardening. Anything that matches on the old message text would break, so grep for those strings (`grep -rn "not a valid path\|repo-relative" internal/`) before finishing. This overlaps with #215 around scan-failed logging, so rebase if it lands first.

### Size
S. Files: `internal/scanner/decode.go`, `internal/scanner/finding.go`, `internal/scanner/finding_test.go`. About 40 to 90 changed lines, mostly tests; the production change alone is small.
