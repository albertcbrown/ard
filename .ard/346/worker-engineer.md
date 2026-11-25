---
role: engineer
status: done
attempt: 1
---
Adds `ard setup`: it runs every check, then applies the `ard:*` label sync only when the permission row is ok. `--check` reports only and exits 2 on drift; `--dry-run` routes writes through the effect layer. `ard labels sync` stays as an alias.

## Changes
- `internal/setupcmd/` (new): `report.go` (ok/changed/drift/manual rows and printing), `setup.go` (`Run`: origin/main warning, permission row, labels row, apply via `labelsync.Sync`), `cmd.go` (`Command` handler, `LocalVsMain`, minimal default permission check).
- `internal/cli`: `SetupOpts`, `Handlers.Setup`, new `cmd_setup.go` with `--check`, registered in `root.go`; `labels sync` help text says it is an alias.
- `internal/labels/labelsync/cmd.go`: `resolveRepo` exported as `ResolveRepo` (text unchanged) and reused by setup.
- `cmd/ard/main.go`: wires `Setup`. `docs/layout.md`: one line for the new package.
- Tests: `setupcmd` setup and cmd tests (apply, idempotent, check, permission failure, dry-run, never deletes, sync failure, warning, row printing, bad config, LocalVsMain); CLI stub and help tests.

## Testing
`go test` on `./internal/setupcmd ./internal/cli ./internal/labels/... ./cmd/...` passes. `go vet` passes. A full `go test ./...` hit the tool time limit here, so it was not completed.

## Open issues
- The permission check is a placeholder (repository reachable via `ListLabels`) until #302 lands; swap it in `Command` then. A maintainer should confirm this split.
- `make lint` reports a gofmt parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file this change does not touch.
