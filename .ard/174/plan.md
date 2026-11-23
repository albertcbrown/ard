# Plan for #174

## Implementation

### Steps
1. `internal/executor/local/local.go` (`chmodWritable`, ~line 321): keep the existing walk, which already skips symlinks (`WalkDir` does not follow them and the symlink branch returns before any chmod), but make it robust. Do not return early on a non-nil `err` for a directory that cannot be read: if `ReadDir` fails because a directory is 0000 or 0555 without x, chmod it first. Use a pre-order approach (chmod the directory before descending): a custom recursive helper `makeRemovable(dir string)` that `os.Lstat`s each entry, `os.Chmod(dir, perm|0o700)` for directories, `0o200` for regular files, skips symlinks, then `os.ReadDir`s and recurses. The reason is that `WalkDir` reads a directory before the callback can chmod it, so a 0555 dir is fine (readable) but a 0000/0444 dir would be missed. Keep the name `chmodWritable(root string)` so the callers do not change. Optionally add a helper `removeAllWritable(path string) error` that calls `chmodWritable(path)` then `os.RemoveAll(path)`.
2. `internal/executor/local/proc_unix.go` (~lines 294-310, `Cleanup`): replace `chmodWritable(filepath.Join(h.Home, sidecarDirName))` with `chmodWritable(h.Home)`, the whole job home, which is where `go/pkg/mod` lives. Use `removeAllWritable` for `h.Worktree` (both RemoveAll calls, lines ~300 and ~305) and for `h.Home`. The Reaper (`internal/reaper/retention.go:61`, `finish.go`) and the worker (`internal/worker/run.go:749`) all go through `Executor.Cleanup`, so no change is needed there. Check `internal/worker/run.go` near line 749 to confirm that the step cleanup uses `Executor.Cleanup`; if it calls `os.RemoveAll` directly, route it through the executor.
3. `internal/executor/local/local.go` (~lines 198-207, failure path of Start): use `removeAllWritable` for `wt` and the `created` paths, for consistency.
4. `internal/executor/local/env.go` (`hostEnv`): add `"GOFLAGS=-modcacherw"` so that modules downloaded by host steps are writable and removable in the future. Keep this addition small. It does not change what the agent can write beyond its own home.
5. Decision on the shared module cache: NOT taken in this change. A writable cache shared across jobs lets one job's agent poison modules that later jobs, and the controller's own checks, build. Mounting it read-only into Docker jobs would need a controller-owned warm-up step, which is a design decision. Recommend a separate issue. Note the decision in the PR description.
6. `docs/proposal.md` (Executors and the Reaper section): add one or two sentences saying that host job steps get `GOFLAGS=-modcacherw` and that cleanup restores owner write permission on the whole job home and never follows symlinks. Say that the module cache is per job and not shared.

### Test plan
Command: `go test ./internal/executor/local/... ./internal/reaper/...` then `make test` and `make lint`.
- `TestChmodWritable_ReadOnlyTree` (`internal/executor/local/misc_test.go` or a new `cleanup_test.go`): create home/go/pkg/mod/x with a 0555 dir holding a 0444 file, and a nested 0555 dir; call `removeAllWritable`; expect nil error and the path gone.
- `TestChmodWritable_UnreadableDir`: a 0000 dir with a file; expect removal.
- `TestRemoveAllWritable_SymlinkNotFollowed`: inside the home, a symlink to a 0555 directory with a 0444 file outside the home; after removal the home is gone, the outside tree still exists and its modes are unchanged (still 0555/0444).
- `TestCleanup_ReadOnlyHome`: using the existing `local_test.go` harness, start a job, plant a read-only module tree under `h.Home/go/pkg/mod`, call `Cleanup`, expect no error and `h.Home` removed.
- `TestHostEnv_GOFLAGS`: `hostEnv` contains `GOFLAGS=-modcacherw` (existing env tests in `local_test.go`).
- Edge cases: a missing path (nil error); running as root (the chmod steps are harmless).

### Risk
Low. The change is confined to the cleanup helpers in `internal/executor/local` and one env var. No protected paths from `merge.protected_paths` are touched (`docs/proposal.md` is not protected). The security-sensitive point is symlink handling; use `Lstat` and never chmod through a link. There are no migrations or public interface changes. `Cleanup` runs concurrently with reaper activity, but each job home is unique, so there is no shared state.

### Size
S to M (about 100 changed lines, including tests). Files: `internal/executor/local/local.go`, `internal/executor/local/proc_unix.go`, `internal/executor/local/env.go`, a test file in `internal/executor/local/`, `docs/proposal.md`.
