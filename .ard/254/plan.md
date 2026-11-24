# Plan for #254

## Implementation

### Steps
1. `internal/redact/rules.go`: add new `func codeRef(v []byte) bool`, true when v is an unquoted Go selector/identifier chain: it matches `^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$` (compile once as a package var `codeRefRE`). Reason: `Token: w.WorkerToken.Value` currently flags (long dotted value, entropy >= 3.5); `Tokens: u.Tokens` and `Token: w.Token` are already short but should be covered at the rule level.
2. `internal/redact/rules.go`: add new `func numeric(v []byte) bool`, true when v is non-empty and all bytes are ASCII digits. Reason: token counts such as a 16+ digit value or counts joined with other numbers.
3. The exclusion must apply only when the value was NOT quoted, so quoted values stay fail-closed. The regex has the opening quote outside capture group 1. Change the regex to capture the quote: `...[:=]\s{0,16}(["']?)([A-Za-z0-9_\-+/=.]{16,250})` (group 1 = quote, group 2 = value), and update the index use in `scanLine` (`internal/redact/scan.go:100`, currently `m[2]:m[3]`) to `m[4]:m[5]`, quote = `m[2]:m[3]`. Keep `Finding` column as `m[0]+1`.
4. `internal/redact/scan.go` `scanLine`: skip the match when `len(quote)==0 && (codeRef(v) || numeric(v))`, in addition to the existing `placeholder`/entropy skips. Do not extend the exclusions to quoted values. Guard: codeRef must require at least one lowercase-or-underscore structure only via the regexp above; do NOT treat a bare identifier with no dot and mixed case/digits of base62 shape as safe if it is 32+ chars with entropy above threshold. Decision: apply codeRef only if the value contains a `.` (selector) or is shorter than 32 bytes (identifier), so a pasted opaque 32+ char alphanumeric secret (`api_key = <base62>`, the existing true positive in `helpers_test.go:34`) still flags. Encode that in codeRef.
5. Update the comment on `generic-assignment` in `rules.go` to describe the new exclusions.

### Test plan
Command: `go test ./internal/redact/...` (repo gate: `make test`).
Add `TestScannerGenericAssignmentFalsePositives` in `internal/redact/scan_test.go` (table, expect zero findings):
- `Tokens: u.Tokens` ; `Token: w.Token` ; `Token: w.WorkerToken.Value`; `TotalTokens: usage.TotalTokens,`
- `"cache_read_input_tokens": 1106888` ; `"tokens": 11068881106888123` (16+ digits)
Add `TestScannerGenericAssignmentTruePositives` (expect a `generic-assignment` finding):
- `api_key = ` + `randStr(13, 32)` (existing planted token, unquoted opaque, no dot)
- `token: "` + randStr(14, 32) + `"` (quoted)
- `password = "` + randStr(15, 24) + `.` + randStr(16, 24) + `"` (quoted dotted)
- `auth="` + digits-only 20 chars + `"`?? only if entropy >= 3.5; otherwise place in negatives (digits only reach about 3.3 bits, so quoted digits stay unflagged by entropy; do not assert it).
- `secret=` + randStr(17, 40) + `.` + randStr(18, 40) (unquoted dotted opaque: documents that dotted base62 mixes with a 40+ char segment still flag; adjust codeRef so a selector requires every segment to be a short identifier, at most 24 bytes, or else this case fails the plan).
Keep `TestScannerPositivePerRule`, `TestScannerNegatives` unchanged and passing. Edge: match at overlong-line seam (`TestScannerOverlongLineSeam`) must still pass after the group renumbering.

### Risk
Medium. Security-sensitive scanner, fail-closed behaviour; the exclusions are narrow and gated on unquoted values. No protected paths (`internal/redact/` is not in merge.protected_paths), no interface or migration changes. The scan is line-wise with no shared state, so no concurrency concerns.

### Size
S to M (about 40 to 90 changed lines). Files: `internal/redact/rules.go`, `internal/redact/scan.go`, `internal/redact/scan_test.go`.
