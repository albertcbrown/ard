---
role: engineer
status: done
attempt: 1
---
Adds a `roles_repo` config key (repo plus optional ref) that loads role directories from a shared repo into the config snapshot; a local directory of the same kind and name wins whole. An omitted ref now follows the shared repo's default branch (remote HEAD), so `master` repos work.

## Changes
- `internal/config`: `roles_repo` parsing and validation, `Resolved.RolesRepo`/`RolesSHA`, `SharedRoles`, `SharedSourcePaths`. The ref is no longer defaulted to `main` and is validated only when set.
- `internal/configsrc`: fetches the shared ref (or `HEAD` when omitted) shallowly into a bare cache, merges missing role dirs into the snapshot named `<sha>+<shared12>`, and fails with nothing published on any shared-repo error.
- `internal/servecmd/serve.go`: passes `SharedDir`.
- `docs/proposal.md`: documents the key, local-wins, the default-branch rule and the trust boundary.
- Tests: config validation and `SharedRoles` cases; `configsrc/shared_test.go` including the new `TestSharedDefaultBranchMaster`.

## Testing
`go test ./internal/config ./internal/configsrc ./internal/servecmd` passes; `go vet` on changed packages is clean; gofmt clean. `make lint` was not run.

## Review findings
- `internal/config/defaults.go:43`, `source.go:52`, `configsrc.go:461`, docs: fixed: the ref stays empty when omitted, `HEAD` is fetched, docs updated.
- `shared_test.go`: fixed: added the master default-branch test.
- `render.go` per-role origin: not changed: the plan allowed dropping it.

## Open issues
- Enabling `roles_repo` in this repo's own `.ard/config.yaml` is a protected-path change for a human.
