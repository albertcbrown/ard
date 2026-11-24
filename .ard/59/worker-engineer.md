---
role: engineer
status: done
attempt: 1
---
Adds a `roles_repo` config key (repo plus pinned ref, validated like sidecars) so role directories can be shared across repositories. The config source merges the shared repo's role directories into the config snapshot, and a local directory of the same kind and name wins whole.

## Changes
- `internal/config`: `roles_repo` parsing and validation, `Resolved.RolesRepo`/`RolesSHA`, `SharedRoles` (reads only that key), `SharedSourcePaths`, rendering in `ard config check`.
- `internal/configsrc`: fetches the shared ref shallowly into a bare cache (`SharedDir`, `SharedURL`), merges missing role directories into the snapshot, and names it `<sha>+<shared12>`. A moved shared ref re-exports even when the local SHA is unchanged. The snapshot is validated before being published, and any shared-repo failure returns an error with nothing published.
- `internal/servecmd/serve.go`: passes `SharedDir` under `~/.ard/roles-repo/<owner>/<repo>`.
- `docs/proposal.md`: Configuration section documents the key, the local-wins rule and the trust boundary (pin a full SHA).
- Tests: config validation cases, `SharedRoles`, and `configsrc/shared_test.go` (merge, local wins, two repos sharing one set, ref moves vs SHA pin, failures, no-roles shared repo, symlink rejection, overrides, unchanged behaviour without the key).

## Testing
`go test ./...` passes. `go vet` on the changed packages is clean. `make lint` did not finish within the tool timeout, so it was not run to completion.

## Open issues
- The per-role origin (`Role.Shared`, `.ard-shared` marker) from the plan was skipped to keep the diff small; `ard config check` shows `roles_repo` but not which roles are shared.
- Enabling `roles_repo` in this repo's own `.ard/config.yaml` is a protected-path change for a human.
