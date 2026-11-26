# Plan for #388

## Implementation

### Steps
1. `internal/redact/rules.go` (new): add `envNameRE = regexp.MustCompile("^[A-Z][A-Z0-9]*(_[A-Z0-9]+)+$")` and `func envName(v []byte) bool` returning true when `len(v) <= 64` and the regexp matches. The pattern needs at least one underscore, allows no lower case and none of the characters plus, slash, equals or dot. Reason: an upper-case snake-case value is an environment variable name, not a secret.
2. `internal/redact/rules.go`: call `envName(v)` from `placeholder()` (before the `placeholderRE` return). Placing it there makes it apply to quoted and unquoted values, which the issue's acceptance criteria require; `codeRef` only covers unquoted values. Update the comment above the generic-assignment rule to mention it. `scanLine` in `scan.go` needs no change. The log scan, the committer scan and the diff guard all call this one scanner, so all three change together.
3. `internal/redact/scan_test.go`: extend `TestScannerGenericAssignmentFalsePositives` with the three env-name cases from the issue (quoted value after an identifier with api key; unquoted value after a token key; a quoted value after a secret-named key holding a private-key env name). Build any credential-shaped input by string concatenation at run time (as `TestScannerGenericAssignmentTruePositives` does) so the diff guard does not hold the worker's own change.
4. `internal/redact/scan_test.go`: add `TestScannerGenericAssignmentEnvNameBoundaries`, a table test (see Test plan).
5. Risk check from the issue: confirm by reading that no other rule's format is upper-case snake case. Reviewed already: all other rules are prefix-anchored (`ghp_` and `github_pat_` are lower-case prefixes, `AKIA`/`ASIA` have no underscore, `sk-`, `xox`, `eyJ`, `AIza` contain lower case or a hyphen), and they run before or independently of the generic rule, so they are unaffected. `TestScannerPositivePerRule` (via `helpers_test.go` fixtures) must still pass unchanged.
6. Docs: `docs/proposal.md` has no paragraph describing generic-assignment, so no doc change is required. Optionally add a one-line note under the log secret-scan text; skip if no fitting paragraph is found.

### Network inputs
None.

### Test plan
Command: `go test ./internal/redact/...`
- Env-name negatives (file `internal/redact/scan_test.go`, `FalsePositives`): the three acceptance inputs produce zero findings.
- `EnvNameBoundaries` table, each scanned in the same assignment position:
  - upper-case value with several underscores, 20 to 64 chars: no finding.
  - 65-char upper-case snake value: still reported when entropy is above 3.5 (length cap).
  - value with no underscore (upper-case letters and digits only, 20+ chars, high entropy): still reported.
  - mixed-case snake value, same length: still reported.
  - lower-case snake value with high entropy: still reported.
  - upper-case snake value with a trailing dot segment, plus, slash or equals: still reported.
  - leading underscore, leading digit, or double underscore: not matched by the regexp, so judged by entropy as before.
- Existing `TruePositives` and `TestScannerPositivePerRule` unchanged and passing.
- Run `go vet ./internal/redact/` and the whole `go test ./...`.

### Risk
Medium. It loosens a security control shared by three paths. Mitigation: narrow pattern, length cap, no existing true-positive test changes. A real secret in upper-case-with-underscores form would now pass the generic rule; vendor formats checked above do not have this shape. No protected paths, migrations or concurrency touched.

### Size
S. Files: `internal/redact/rules.go`, `internal/redact/scan_test.go`.
