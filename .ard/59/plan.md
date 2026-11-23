# Plan for #59

## Implementation

### Steps

Approach: keep `Role.Dir`, `PromptPath` and `Dockerfile` relative to the snapshot (`Resolved.Repo`), as `agentjob/command.go`, `worker/run.go` and `scanner/engine_exec.go` assume. The shared roles are therefore merged into the snapshot directory on disk before `config.Load` runs, and no consumer of `Role` changes.

1. `internal/config/types.go`, `raw.go`: add a top-level `roles_repo: { repo: <owner>/<name>, ref: <ref> }` key (new). Add `RolesRepo *Sidecar` to `Resolved` (reuse the `Sidecar` type: `Repo`, `Ref` default `main`). Parse it in `rawConfig` beside `parseSidecars`, with the same validation: `validRepo`-style `owner/name` check and `gitref.Check(ref)`, unknown keys rejected. Reason: same pinning rules as sidecars.
2. `internal/config/source.go` (new func): `SharedRoles(fsys fs.FS) (*Sidecar, error)` reads only `roles_repo` from `.ard/config.yaml`, like `LocalPaused`. `configsrc` needs it before the full load, because the shared roles have to be on disk before discovery. Add `SharedSourcePaths()` returning the four `.ard/<kindDir>` trees, built from `kindOrder` and `kindDir`.
3. `internal/configsrc/configsrc.go`: extend `Git` with `InitBare` and `FetchShallow` (`*gitx.Git` already has both) and add `Options.SharedDir` (a bare cache outside the snapshots) and `SharedURL func(repo string) string` (default `https://github.com/<repo>.git`, tests override it). In `Load`, after resolving the local SHA, export the config at that SHA to a temporary dir and call `SharedRoles`. When `roles_repo` is set, fetch it shallow into `SharedDir`, resolve it to a SHA and export `SharedSourcePaths()` to a temp dir. Then copy each `<kind>/<name>` directory that does NOT already exist in the local export into it. The local directory of the same name wins and is not merged file by file. Publish the snapshot under `<sha>` when there is no shared repo, else `<sha>+<shared12>`. `Resolved.SHA` stays the local SHA; add `Resolved.RolesSHA`. Re-export when the shared SHA changes even though the local SHA does not (the cache check compares both). `prune` already skips `.tmp-` names. A shared-repo failure returns an error, so callers treat it as paused and nothing runs from a half-resolved role set. Dot-names and all-digit directories stay skipped by `discover`.
4. `internal/config/render.go` and `testdata/resolved.golden`: render `roles_repo` and each role's origin (`local` or `shared`) so `ard config check` shows the winner. The origin is a new `Role.Shared bool`, set when `configsrc` writes a `.ard-shared` marker listing the copied dirs. Keep this small; skip it if it grows the diff.
5. `internal/servecmd/serve.go` (around line 186): pass `SharedDir` (under `ardRoot`, e.g. `<root>/roles-repo/<owner>/<repo>.git`, built with `repopath.Join`) and `Token: w.Token` for private shared repos.
6. `docs/proposal.md`, Configuration section: document `roles_repo`, the local-wins rule, the pin rules and that `ard serve` re-reads it with the config. Document the trust boundary: the pinned ref lives in `.ard/config.yaml` (protected), but a branch or tag in the shared repo can change roles without a PR here, so recommend a full SHA.

### Test plan

Command: `make test` (`go test ./...`), then `make lint`.

- `internal/config/load_test.go`: `roles_repo` valid with and without `ref` (default `main`); bad repo `foo`; bad ref; unknown subkey; wrong type. Each gives an `Error` with key `roles_repo...`.
- `internal/config/source_test.go`: `SharedRoles` on a missing file returns nil, nil; on a file with the key returns the value; on invalid YAML returns an error.
- `internal/configsrc/configsrc_test.go`, using local bare repos and a `SharedURL` pointing at them:
  - `TestSharedRolesMerged`: the shared repo has `triage/lead` and `workers/engineer`; the local repo has neither. Both appear in `Resolved.Roles` with working paths under the snapshot.
  - `TestLocalRoleWins`: both repos have `workers/engineer`. The local prompt is the one in the snapshot, and none of the shared files (extras, Dockerfile) leak in.
  - `TestTwoReposShareOneRoleSet`: two local repos with different `roles_repo`-less roles and the same pinned shared repo each resolve the shared set, one with an override (the issue's done condition).
  - `TestSharedRefMoves`: the branch ref advances while the local SHA is unchanged. A new snapshot is exported and `RolesSHA` changes. A full-SHA pin does not change.
  - Failures: unknown shared ref, unreachable repo, shared role with an invalid prompt (empty `prompt.txt`) all return an error, with no snapshot published.
  - `TestNoRolesRepoUnchanged`: existing behaviour, snapshot named `<sha>`.
- Edge cases: a shared repo with no `.ard/` returns no roles and no error; a symlink in the shared repo is rejected by `ExportPaths`; a config `overrides` entry naming a shared role applies; one naming a missing role still errors.

### Risk

Medium. Role directories are trust-sensitive: they hold the prompts agents run. A shared-repo change takes effect without a PR in this repo, so the pin is the control. Nothing agent-editable changes: `roles_repo` is in `.ard/config.yaml`, which is in `merge.protected_paths`. It touches the config load path that every loop depends on, but it is additive and a no-op when the key is absent. Concurrency: `configsrc.Source` already serialises `Load` under a mutex. There are no migrations and no public interface changes apart from the new config key. Protected paths: the code change itself touches none of them (`internal/`, `docs/`). Enabling `roles_repo` in this repo's own `.ard/config.yaml` is a separate human step.

### Size

L. Roughly 450–700 changed lines, mostly tests. Files: `internal/config/types.go`, `raw.go`, `source.go`, `render.go`, `testdata/resolved.golden`, `load_test.go`, `source_test.go`; `internal/configsrc/configsrc.go`, `configsrc_test.go`; `internal/servecmd/serve.go`; `docs/proposal.md`.
