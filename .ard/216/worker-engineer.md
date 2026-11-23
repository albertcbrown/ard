---
role: engineer
status: done
attempt: 1
---
Adds the starter `scanners/trufflehog` role: a fail-closed scan of the current commit for leaked credentials, with only detector, file, line and a hash of the secret leaving the scan. The role is wired into the starter config with `route: needs-human`, and the `ard init` PR body gains an opt-out note.

## Changes
- `internal/roles/scanners/trufflehog/`: `scan` (POSIX sh, 0755), `findings.jq` (project and merge stages), `Dockerfile` (cve's pinned base, trufflehog and jq checksums), `exclude-paths`.
- `internal/roles/config.yaml`, `roles.go`: override line with `route: needs-human`; `go:embed` and doc comment list the role.
- `internal/initcmd/body.go`: fixed PR-body note on what the role does, the AGPL-3.0 binary, and how to opt out.
- Tests: `roles_test.go` (file list, exec bit, load test), `initcmd_test.go` (tree, PR body), new `trufflehog_test.go`.
- `docs/proposal.md`, `CLAUDE.md`: table row, paragraph, config example, layout line.

## Testing
`go test ./internal/...` passes. New tests use a fake `trufflehog` and cover golden fingerprints, aggregation, input-order determinism, no secret or marker in stdout or stderr, an empty temp dir, 15 fail-closed cases, exact argv with no forbidden flags, a missing tool (exit 2), and the Dockerfile pins.

## Open issues
- The trufflehog checksums in the Dockerfile were copied from the issue's plan text; there is no network here. A human should re-verify them against the signed release checksums.
- `scan` and `findings.jq` handle live secrets and need a line-by-line human read. `CLAUDE.md` is a protected path.
- Not written: the allow-file positive test, the exclude-paths file test, and the killed-mid-stream and `Raw`-adjacent canary subtests. The canary check covers `RawV2` and `Redacted` only.
- Edge: a tree where every file is excluded fails closed.
