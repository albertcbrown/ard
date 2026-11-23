---
role: engineer
status: done
attempt: 1
---
Adds the starter `error-codes` scanner: a `sh` + `jq` role that reads an optional human-owned `codes.json` and reports duplicated, unregistered and undocumented error codes. It fails the run instead of returning a shorter list, because the scanner is `autoclose`.

## Changes
- `internal/roles/scanners/error-codes/`: `scan` (POSIX sh, 0755), `errcodes.jq` (settings, classify, match and findings modes) and `Dockerfile` (same base digest and jq checksums as the cve one, no Go tools).
- `internal/roles/config.yaml`: override `every: 24h, group_size: 20, autoclose: true`. `roles.go`: doc comment.
- Tests: new `errcodes_test.go` (scan table, failure table, determinism, symlinked cwd, glob table, Dockerfile pin), `errcodes_integration_test.go` (Docker, `integration` tag), plus `roles_test.go` and `initcmd_test.go` updates.
- `docs/proposal.md` and `CLAUDE.md`: scanner description, table row, init list and override.

## Testing
`go vet ./...`, `go vet -tags integration ./internal/roles/` and `go test ./internal/roles/ ./internal/initcmd/ ./internal/config/` all pass on macOS (BSD tools, jq 1.8.2).

## Open issues
- The Docker integration test was not run (no Docker here), and the base-image digest was copied from the cve Dockerfile, not re-verified. A human should run both before merge.
- `find -size -1025k` rounds differently on BSD and GNU, so the 1 MiB file cap is approximate to within a kilobyte; the test uses a clearly larger file.
- `CLAUDE.md` is a protected path, so the PR waits for human approval. Its layout line was listed once, not twice.
- `make test` was not run; the three affected packages were.
