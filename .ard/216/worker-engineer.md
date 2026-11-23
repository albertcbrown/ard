---
role: engineer
status: done
attempt: 1
---
Adds the starter `scanners/trufflehog` role: a fail-closed scan of the working tree that files one needs-human issue per leaked credential, never emitting the secret itself. This round adds the missing `allow` test and widens the canary check.

## Changes
- `internal/roles/scanners/trufflehog/`: `scan`, `findings.jq`, `exclude-paths`, `Dockerfile`.
- Starter config, `roles.go`, `ard init` PR body note, docs.
- Tests: `roles_test.go`, `initcmd_test.go`, `trufflehog_test.go`.

## Testing
`go test ./internal/roles/` passes. Added `TestTrufflehogAllow` (no `allow` keeps both findings; an `allow` file with a comment, a blank line and an inline comment drops the listed fingerprint and the count check still passes). The canary fixture now also carries `SecretParts`, `ExtraData`, `StructuredData`, `VerificationError` and `repository` markers.

## Review findings
- `trufflehog_test.go` line 566 (blocking): fixed: added `TestTrufflehogAllow`.
- `trufflehog_test.go` line 600: fixed: more canary fields. A killed-mid-stream case was not added.
- `trufflehog_test.go` line 683: not changed: the `-x` file contents test, the Go golden recomputation and the 60-location case are not added; they are non-blocking.
- `scan` line 429: no action, as the reviewer said.

## Open issues
- The trufflehog Dockerfile checksums could not be verified offline; a human should check them against the signed release checksums.
- The role touches the protected docs path; a human must approve.
