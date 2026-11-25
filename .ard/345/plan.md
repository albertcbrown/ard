# Plan for #345

## Implementation

### Steps
1. `internal/setupcmd/cmd.go` (edit): move the inline `Permissions` closure into a new exported `func CheckPermissions(ctx context.Context, r labelsync.Reader, repo github.Repo) Row` (same behaviour: `ListLabels` reachable gives `StatusOK`, else `StatusManual` with the credentials hint). `Command` calls it. No shared check exists yet (#302 is not on main), so this is the one place both commands use; #302 later replaces its body. Existing setupcmd tests must still pass.
2. `internal/cli/app.go` and `internal/cli/cmd_init.go` (edit): add `PR bool` to `InitOpts`; add `--pr` bool flag in `newInit` (default false); update `Short` to "Write the starter .ard/ directory (--pr opens a PR instead)". Update `internal/cli/commands_test.go` if it asserts init flags/help.
3. `internal/initcmd/local.go` (new): `func runLocal(ctx, e *cli.Env, o cli.InitOpts, root string, open func(ctx, root) (deps, error)) error`.
   - Refuse if `.ard/` has uncommitted changes: new `gitx` call or `git status --porcelain -- .ard/` via existing gitx helpers (check `internal/gitx` for a status/`StagedPaths`-style method; add a small `DirtyPaths(ctx, root, pathspec)` there if none). Error names the paths and says to commit or stash. Untracked files under `.ard/` count as changes.
   - Plan against the local tree: `existing := os.DirFS(root)` restricted to `config.SourcePaths()`, `ContextPaths()`, `LockPath` (reuse `plan`, `changed`, `withLock`, `ParseLock`, `overlay`, `config.LoadFS` exactly as in `run` steps 6). Invalid merged config fails before any write.
   - Write: for each `changed(ops)` file plus the lock, `os.MkdirAll` the parent and write atomically (temp file in same dir, `os.Rename`), mode 0644 or 0755 when `Exec`. Guard every target: path must start with `.ard/`, be clean, and resolve inside `root/.ard` (reject symlinked parents with `os.Lstat`). Never delete anything (dropped files are only reported).
   - `--dry-run` (`e.DryRun`): print the same `printOps` lines ("would add ...") and `printLeftAlone`, write nothing.
   - Permission check: when repo and credentials resolve, call `setupcmd.CheckPermissions`; print `warning: permissions: <detail>` on a non-ok row and continue. If repo/auth cannot be resolved, print a warning and continue. Open the client only for this read.
   - End by printing next steps: commit `.ard/`, merge it to main, run `ard setup`. When nothing changed print "nothing to write" and still print the next steps only if the lock or files differ from HEAD (otherwise just `ard setup`).
4. `internal/initcmd/initcmd.go` (edit): in `Init`, branch on `o.PR`; `run` becomes the `--pr` lane. In `run`: remove step 7 (`labelsync.Sync`/`Print`, the `labels` import and the `want` computation); add the permission check after `open` and make a non-ok row return an error `ard init: permissions: ...` before any push (in `--pr` mode it fails). Update the package doc comment. Keep `checkAuth` for `--pr` only; local mode must not need a GitHub App. After the PR print the next step: merge, then `ard setup`.
5. `internal/initcmd/body.go` (edit, only if it mentions labels): drop label wording from the PR body.
6. `docs/proposal.md` (edit, the paragraph at the `ard init` section, line about 503): remove the sentence saying init behaves as before until #344 and #345 land; describe local default, `--pr`, labels moving to setup, and the permission check (warn locally, fail in `--pr`).

### Test plan
Command: `go test ./internal/initcmd/... ./internal/setupcmd/... ./internal/cli/...` then `go vet ./...`.
- `initcmd/local_test.go` `TestLocalWritesTree`: clean git clone, no `.ard/`; run local. Expect starter files and `ard-init.lock` on disk, exec bits right, nothing outside `.ard/`, no GitHub writes on the fake, next steps printed.
- `TestLocalRefusesDirty`: modify a tracked file and, separately, add an untracked file under `.ard/`. Expect an error naming the path; nothing written.
- `TestLocalDryRun`: expect "would add" lines, tree unchanged.
- `TestLocalNeverDeletes`: lock lists a dropped file and an edited file differs; both stay, edited is reported with a diff.
- `TestLocalSecondRunNoop`: rerun after commit writes nothing.
- `TestLocalInvalidConfigWritesNothing`; `TestLocalSymlinkEscape` (a `.ard` symlink or symlinked subdirectory pointing outside is rejected).
- `TestLocalPermissionWarns`: fake reader whose `ListLabels` fails; local run succeeds with a warning. Also no credentials: still succeeds.
- Update existing `initcmd_test.go` tests: `TestInitLabelSyncFailureStopsBeforePush` becomes `TestInitPRPermissionFailureStopsBeforePush`; `TestInitOpensPR` and others assert no labels were created on the fake.
- `cli` test: `--pr` flag parsed into `InitOpts.PR`.
- `setupcmd`: test for `CheckPermissions` ok and manual rows.

### Risk
Medium. It changes the default behaviour of a public command and writes to the user's working tree (path escapes and partial writes are the main hazards, hence the root guard and atomic renames). It removes label sync from init, so it should land with part D, which is already on main. No protected path in `merge.protected_paths` is touched; the code only writes under `.ard/` at run time. The permission check is a stand-in until #302.

### Size
L (roughly 450 to 600 changed lines including tests). Files: `internal/setupcmd/cmd.go`, `internal/cli/app.go`, `internal/cli/cmd_init.go`, `internal/initcmd/initcmd.go`, `internal/initcmd/local.go` (new), `internal/initcmd/body.go`, `internal/gitx/*.go` (possible small helper), tests in `internal/initcmd`, `internal/setupcmd`, `internal/cli`, and `docs/proposal.md`.
