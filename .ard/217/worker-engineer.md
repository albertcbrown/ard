---
role: engineer
status: done
attempt: 1
---
Adds host and Docker integration tests that prove the trufflehog scanner role against a real binary and its own image, and fills the remaining gaps in the proposal's trufflehog paragraph. No controller code changes.

## Changes
- `internal/roles/trufflehog_integration_test.go` (integration tag): `TestTrufflehogReal` builds a run-time read-only repo with randomly generated, split-prefix values: one key in two files, a credentialed URI, a value behind an ignore tag, a value under an excluded path, and a symlink to a secret outside the tree. It checks an in-process oracle for fingerprints, the structure, that no planted value reaches stdout, stderr, HOME or TMPDIR, that `redact.Builtin()` is quiet, and that a second run is identical. It fails (never skips) when `ARD_TRUFFLEHOG_BIN` is not the Dockerfile's pinned version.
- `internal/roles/trufflehog_docker_integration_test.go` (integration tag): `TestTrufflehogDocker` builds the role image and runs it with `--network none`, a `:ro` mount and `--cap-drop ALL`. It makes the same leak checks, requires an identical second run, and compares with a host run when the binary is set.
- `docs/proposal.md`: the trufflehog paragraph now covers HEAD-only scanning (#224), the forbidden flags, not-revoked-when-gone, the unsalted hash, `file:line` in public issues, the backpressure caveat (#221), the scanner-log commit exclusion, the sole-detection-channel caveat, and the AGPL-3.0 licence.

## Testing
- `make lint test`: pass.
- `go vet -tags integration ./internal/roles/`: clean.
- `ARD_TRUFFLEHOG_BIN=$(which trufflehog) go test -tags integration -run TrufflehogReal ./internal/roles/` with trufflehog 3.100.0: PASS. With `ARD_TRUFFLEHOG_BIN=/bin/echo` it fails on the version, as intended.
- `TestTrufflehogDocker` was not run here, because I did not try Docker.

## Open issues
- A maintainer needs to run `TestTrufflehogDocker` with Docker and network, and paste the PASS lines into the PR.
- CLAUDE.md is unchanged: its Layout section points to `docs/layout.md`, which already lists the role, as the plan says. The label table already covers `ard:needs-human` at filing.
