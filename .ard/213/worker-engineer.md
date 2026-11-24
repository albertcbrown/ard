---
role: engineer
status: done
attempt: 1
---
Wires the missing-tests helper into a starter scanner: a scan script and pinned Dockerfile, a starter config entry, the starter lists in the tests, and the docs.

## Changes
- `internal/roles/scanners/missing-tests/scan` (0755 sh): builds the helper, finds Go modules, runs `go list` and `go test` with coverage per module, and prints findings last. A failing build or test exits 1 with no output; missing `go` exits 2. Honors human-owned `ignore` and `no-coverage` files.
- `internal/roles/scanners/missing-tests/Dockerfile`: pinned Go image, same base as the docs scanner.
- `roles.go`: embed line and doc comment. `config.yaml`: `every: 24h, max_open: 5, group_size: 10, autoclose: true`.
- Tests: starter lists in `roles_test.go` and `initcmd_test.go`, a role-load test, scan end-to-end tests over every fixture (decode and determinism checked), failure, no-module and nested-module cases, and a Docker integration test behind the `integration` tag.
- Docs: `docs/proposal.md` (table row, paragraph, overrides example, init list) and `docs/layout.md`. `CLAUDE.md` was not touched; the layout list moved to `docs/layout.md`.

## Testing
`GOPROXY=off go test ./internal/roles ./internal/initcmd` passes. `go vet -tags integration ./internal/roles` is clean. The Docker integration test was compiled but not run (no Docker or network here).

## Open issues
- The scan runs without `GOPROXY=off`, so repositories with dependencies need egress to the module proxy.
- The scan tests substitute the real Go version for `go1.test` in each fixture's expected output.
- The roles test package now takes about 110 s because each fixture runs `go test`.
