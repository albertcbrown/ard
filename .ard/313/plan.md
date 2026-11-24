# Plan for #313

## Implementation

Move the filesystem half of `internal/executor/local` into a new `internal/executor/workspace`, with no behaviour change, following the owner's design revision 3 (latest issue comments). `CopyRole` from the issue body is dropped by that revision, and `GOFLAGS=-buildvcs=false` for Docker is already in `internal/executor/local/docker.go:80` (`runArgv`), so no change is needed there. Line numbers below are from the current tree. `local.go` is 486 lines, and the Prepare body is at 168-333.

### Steps

Each step must leave `go build ./... && go vet ./...` passing.

1. **New `internal/executor/workspace/fs.go`**: move `chmodReadOnly`, `chmodWritable` and `removeAllWritable` (local.go:335-378), exported as `ChmodReadOnly`, `ChmodWritable` and `RemoveAllWritable`. Switch the `local` call sites to the exported names. This is the leaf piece with no dependencies.
2. **New `workspace/layout.go`**: move local/layout.go (WorktreePath, homePath, logDir, LogBase) as the exported `WorktreePath`, `HomePath`, `LogDir` and `LogBase`, as functions or methods of `Workspace` (they use the root). Keep `local.LogBase` and `(*Local).WorktreePath` as one-line wrappers so existing callers stay stable.
3. **New `workspace/populate.go`** (the pod-safe core, stdlib and `executor` only):
   - `type PopulateSpec struct{ Tree, Home string; Scrub []string; Inputs []executor.File }`
   - `type Populated struct{ Scrubbed []string; ConfigDir string }`
   - `func Populate(s PopulateSpec) (Populated, error)`: `scrub` (local.go:435), then inputs through `os.OpenRoot` and `rejectSymlink` (local.go:232-252, 473), then `MakeHome` unless `Home == ""`.
   - `func MakeHome(home string) (configDir string, err error)`: `home/tmp` 0700, and the `.claude` config dir forced to 0700 (local.go:253-266).
   - `const ConfigDirName = ".claude"` (local.go:164).
   - Move `scrub` and `rejectSymlink` here.
4. **New `workspace/sidecar.go`**: move local/sidecar.go whole (`sidecarDirName`, cache, `lockSidecar`, `materialise`, `permanentFetch`). The locks map and mutex move onto `Workspace`. Factor the SHA path out as `func FetchPinned(ctx, g SidecarGit, gitDir, url, sha string, tok gitx.TokenFunc) error` (init the bare repo if there is no `HEAD`, `FetchShallow "+<sha>:refs/ard/pin"`, `RevParse == sha`). `materialise` calls it when `gitref.IsSHA(ref)`. Export `SidecarGit` (was `sidecarGit`) and `SidecarCache(root, jobRepo, scRepo string) string` for the tests (sidecar_test.go:82).
5. **New `workspace/workspace.go`**:
   - `type Git interface{ RevParse; WorktreeAdd; WorktreeRemove; WorktreePrune }` with the same methods as `local.gitRunner`.
   - `type Options struct{ Root string; Git Git; SidecarGit SidecarGit; SidecarURL func(string) string; SidecarToken gitx.TokenFunc; SidecarTimeout time.Duration; CloneDir string; Now func() time.Time; Log *slog.Logger }`.
   - `func New(o Options) (*Workspace, error)`, with the defaults and the absolute-root check copied from `local.New` (local.go:92-146). `execGit` stays in local/git.go and is passed in as `Options.Git`, so `workspace` does not import `local`. `local.New` keeps its own defaults for `Git`.
6. **New `workspace/prepare.go`**:
   - `func (w *Workspace) Prepare(ctx, j executor.Job) (executor.Handle, error)` is local.go:168-300, with `Populate` replacing the scrub, inputs and home block. It returns `Kind "host"` and the host `View`.
   - The rollback defer (local.go:203-219) moves with it. **Keep the order**: worktree removal with the writable fallback, then `created` removal, then the handle reset. `created` must still get home (appended after `MakeHome`, as before) and both log files.
   - `func (w *Workspace) Discard(ctx, h executor.Handle)` removes everything Prepare made, logs included. Local calls it when `ensureImage`/`ensurePulled` or `ChmodReadOnly` fails after `w.Prepare` succeeded.
7. **New `workspace/remove.go`**: `func (w *Workspace) Remove(ctx, h executor.Handle) error` is the `!o.KeepWorktree` body of `Cleanup` (proc_unix.go:299-321) verbatim (foreign-repo branch, `WorktreeRemove` with fallback, home, prune). `Prune` moves from local.go:402-429.
8. **Rewire `internal/executor/local`**:
   - `Local` gets `ws *workspace.Workspace`, built in `New`. Drop the moved fields (`sgit`, `sidecarURL`, `sidecarToken`, `sidecarTimeout`, `cloneDir`, `sidecarLocks`) as they become unused.
   - `Options` is unchanged. `Options.Git` and `Options.SidecarGit` become aliases or assignable to `workspace.Git`/`workspace.SidecarGit`, so the tests' fakes still fit.
   - `Prepare` becomes `l.ws.Prepare` plus the Docker/ReadOnly tail (local.go:301-331), with `Discard` on tail failure.
   - `Cleanup` keeps stop and `docker rm`, then calls `l.ws.Remove` unless `KeepWorktree`. `Prune` delegates.
   - Note `l.git` is still used by other local code, so check before removing it.
9. **New `internal/executor/envpolicy.go`** (package `executor`): move `baseNames`, `ghRE`, `secretRE` and `ardEnv` (local/env.go:14-38) and `checkEnv`/`checkFixed` (env.go:55-105) as `type EnvPolicy struct{ AllowEnv, AllowFixed map[string]bool }` with `CheckEnv([]string) error`, `CheckFixed([]string) error` and `func ArdEnv(Handle) []string`. `checkConfigDir` stays in local. Update the callers, including `docker.go:81` and the tests that call `ardEnv` (local_test.go:808-827).
10. **`docs/layout.md`**: add a line `internal/executor/workspace/` (job worktree, home, config dir, inputs, sidecar export, logs, removal; shared by local and kube). Change the `internal/executor/local/` line (layout.md:18) to "processes, Docker, tracking; calls workspace". Do not touch `docs/proposal.md`: the `-buildvcs=false` clause belongs to a change that is already in place, so check whether it is already documented and add one clause only if it is missing.

### Test plan

Command: `go test ./internal/executor/... ./internal/gitx/...`, then `go test ./...` and `go vet ./...`.

Behaviour tests stay in `local`: `local_test.go`, `sidecar_test.go`, `configdir_test.go` and `cleanup_test.go` drive `local.New(...).Prepare/Cleanup` and must pass with only mechanical edits (the `sf.l.sidecarCache` access goes through `workspace.SidecarCache`, and `ardEnv` becomes `executor.ArdEnv`). That is the no-behaviour-change proof.

New or moved:
- `workspace/populate_test.go` `TestPopulate` (table, plain temp dirs, no git):
  - scrub of a file, a symlink and a missing path succeeds.
  - scrub through a symlinked parent fails, and scrub of a directory fails.
  - an input over a committed symlink fails.
  - nested input directories are created.
  - home/tmp and `.claude` are 0700 under umask 077 and 022.
  - `Home: ""` makes no home.
  - `Scrubbed` echoes the list.
- `workspace/sidecar_test.go` `TestFetchPinned` against a `gitx/gittest` bare remote: the right SHA is fetched, a SHA the remote lacks gives the not-found error, and a second call reuses `gitDir`.
- `workspace/prepare_test.go` `TestDiscard`: worktree, home and both log files are gone, and prune is recorded by the fake git. Also `TestPrepareRollback`: a failure after logs are created removes home and logs.
- `workspace/remove_test.go`: move the `TestRemoveAllWritable_*` cases from cleanup_test.go, and `TestLayout` from misc_test.go.
- `executor/envpolicy_test.go` `TestEnvPolicy` (table): two credentials rejected, `GH_TOKEN` rejected, `ARD_X` rejected, a name not allowed rejected, a secret-shaped Fixed name rejected, and allowed names accepted.
- `local/misc_test.go` `TestRunArgv` already expects `GOFLAGS=-buildvcs=false` (agentimage_test.go:182). Leave it as it is.

Failure cases: Discard on a partly prepared handle, and Remove with a foreign `RepoDir` makes no git call.

### Risk

Medium. This is a mechanical move of security-relevant code (scrub, the input symlink guard, rollback order, sidecar cache locking, 0700 forcing). It touches no protected paths from `merge.protected_paths` (`.github/`, `.ard/config.yaml` and the `.ard/` role directories). `local.Options`, `local.LogBase` and `(*Local).WorktreePath` stay unchanged, but `executor` gains a public `EnvPolicy`. Concurrency: the sidecar lock map must move with its mutex, and `Local.mu` must still guard `tracks`. The unchanged `local` tests guard it.

### Size

L (about 1,300-1,400 changed lines, since a move counts twice under `--no-renames`). It stays under the 2,000-line warning only if `execGit` and the behaviour tests stay in `local`. Files touched:
- `internal/executor/workspace/{fs,layout,populate,sidecar,workspace,prepare,remove}.go` (new)
- `internal/executor/workspace/*_test.go` (new)
- `internal/executor/envpolicy.go` and its test (new)
- `internal/executor/local/{local,layout,sidecar,env,docker,proc_unix}.go`
- `internal/executor/local/{local,sidecar,cleanup,misc}_test.go`
- `docs/layout.md`
