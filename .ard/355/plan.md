# Plan for #355

## Implementation

### Steps
1. `internal/redact/rules.go`: add an optional field `skip func(match []byte) bool` to `rule` (new). Add a new function `awsExample(m []byte) bool` that returns true only when `len(m) == 20` and `bytes.HasSuffix(m, []byte("EXA"+"MPLE"))`. The match is already the whole `\b`-bounded token, so the check is per match, never per line. Set `skip: awsExample` on the `aws-access-key` rule only. Write the suffix as a concatenation so the source has no literal example value. Reason: AWS's documented placeholder ids end in the suffix; every other `AKIA`/`ASIA` id stays a finding.
2. `internal/redact/scan.go`, `scanLine`, non-generic branch: inside the `FindAllIndex` loop, `if ru.skip != nil && ru.skip(data[m[0]:m[1]]) { continue }` before `emit`. Other matches on the same line are still judged, so an exempt token cannot hide a real key.
3. Secret access key: no code change. `generic-assignment` already skips values matching `placeholderRE` (case-insensitive "example"), which covers the documented secret. Cover it with a test, and add a comment on `awsExample` saying so.
4. `internal/servecmd/serve_test.go` (~line 380, `TestRedactText`-style test): the fixture `secret := "AKIA…EXAMPLE"` stops being a scanner hit, so the leak assertion would no longer exercise redaction. Replace it with a runtime-built real-shaped id, e.g. `"AK" + "IA" + "QZ7K2M9TXW4HPN6B"` (16 chars from `[0-9A-Z]`, last 7 not the example suffix). Never write the example value literally.
5. `internal/redact/doc.go`: one sentence documenting the exemption (id with suffix "EXAMPLE", 20 chars, aws-access-key rule only).

Redaction (separate from the scan): unchanged. `secrets.go` redacts known secret values and encodings, not rule matches, so the exemption only affects scan findings. Gitleaks (`internal/redact/gitleaks.go`) is an optional separate scanner that runs the external binary; it is left alone. If it flags the placeholder on its own, that is a separate decision.

### Test plan
Run `go test ./internal/redact/... ./internal/servecmd/...` (repo-wide: `make test`, i.e. `go test ./...`). Add `TestScannerAWSExample` in `internal/redact/scan_test.go`, table-driven; build all values at runtime (join parts) so no literal match appears in source:
- `example id`: `"AK"+"IA"+"IOSFODNN7"+"EX"+"AMPLE"` alone on a line gives no findings.
- `asia example suffix`: `"AS"+"IA"+"ABCDEFGHI"+"EX"+"AMPLE"`, no findings.
- `example secret`: `aws_secret_access_key = ` plus the documented secret built from parts (ends `EXAMPLEKEY`), no findings.
- `real id`: `"AK"+"IA"+` 16 random `[0-9A-Z]` (use `randStr` from `helpers_test.go`, forced to not end in the suffix), 1 finding with rule `aws-access-key`.
- `example then real on one line`: example id, space, real id, exactly 1 finding and its Col points at the real id.
- `real then example`: reverse order, 1 finding.
- `example inside longer token`: example id followed by `X` (21 chars, no `\b`), and `AKIA`+16 chars+`EXAMPLE` (too long), no finding for the first, and a real key elsewhere on the line is still found.
- `suffix case`: id ending in lowercase `example` is not a valid id shape and is not exempt; assert it behaves as before.
- Existing `TestScannerPositivePerRule` must still pass (its `aws-access-key` token is random).
- Update the servecmd test per step 4 and confirm it still sees a hit.

### Risk
Low to medium. This loosens a security control, but only for 20-character ids ending in the fixed suffix, per match, within one rule. No protected paths from `merge.protected_paths` are touched. No public interface changes (`rule` is unexported). No concurrency or migration impact. Reviewer should confirm that one exempt token cannot mask another match on the line (covered by the mixed-line tests).

### Size
S (about 40 to 80 changed lines including tests, so S to M). Files: `internal/redact/rules.go`, `internal/redact/scan.go`, `internal/redact/scan_test.go`, `internal/redact/doc.go`, `internal/servecmd/serve_test.go`.
