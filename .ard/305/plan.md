# Plan for #305

## Implementation

Finding from reading the code: every per-repo root already goes through `repopath.Join` (`<root>/<owner>/<repo>`, ASCII-lowercased, validated), so no collision is expected and the plan is mainly one proof test plus a lock test. The roots are `state.Dir` (`internal/state/dir.go`), `controller.LockPath` (`internal/controller/lock_unix.go`, suffix `.lock`), `clonePath` (`internal/servecmd/clone.go`, suffix `.git`), `repoWorkDir` (`internal/servecmd/scanners.go`), the local executor's `repoDir("work"|"logs")` (`internal/executor/local/layout.go`), and the inline `config` and `roles-repo` roots in `internal/servecmd/serve.go` (the shared-roles cache has landed: `roles-repo`, `SharedDir`). The `.git`/`.lock` suffixes are the only place a collision could arise (`a/b` vs `a/b.git`); they yield `b.git` vs `b.git.git`, which are distinct.

### Steps
1. New file `internal/servecmd/isolation_test.go` (package `servecmd`, so unexported `clonePath` and `repoWorkDir` are reachable). Add `func repoPaths(home string, repo github.Repo) (map[string]string, error)` (test-only helper) returning one path per root: `state` and `state-dryrun` via `state.Dir(filepath.Join(home,".ard"), repo.String(), dryRun)`; `lock` via `controller.LockPath(home, owner, name)`; `clone` via `clonePath`; `work` via `repoWorkDir`; `logs`, `config`, `roles-repo` via `repopath.Join(filepath.Join(home,".ard",<kind>), repo.String())`. Use `repopath.Parts` for owner/name. (If `LockPath` is only defined under `//go:build unix`, put the lock parts in a `_unix_test.go` file.)
2. In the same file add `TestRepoPathsDistinct`: a table of pairs that must differ in every root, and also must not be a path prefix of one another: `a-b/c` vs `a/b-c`, `a/b` vs `a/b.git`, `a/b` vs `a/b.lock`, `a/b.git` vs `a/b.git.git`, `a.b/c` vs `a/b.c`, `a_b/c` vs `a/b_c`, `a_b/c` vs `a-b/c`, `a/b_c` vs `a/b-c`, `o/.github` vs `o/github`. Assert no path of repo X equals any path of repo Y across all roots (cross-root comparison too, so e.g. a clone path never equals a work path).
3. Add `TestRepoPathsCaseFold`: `Owner/Repo`, `owner/repo` and `OWNER/REPO` produce identical maps; also `A/B.git` equals `a/b.git`.
4. Add `TestServeLocksPerRepo` (unix build tag): with one `t.TempDir()` as home, acquire `controller.AcquireLeader` for `a/b` and `c/d` (both succeed, release via the returned `Leader`'s release method; check its name in `lock_unix.go`); a second acquisition for `a/b` fails with `errors.Is(err, controller.ErrLeaderHeld)`; one for `A/B` (different case) also fails; after release, it succeeds. Note: flock on the same file from a second open in the same process conflicts, so the test needs no subprocess (the existing `TestLeaderExcludesSecondAndReleases` in `internal/controller/lock_unix_test.go` does the same).
5. Only if a test fails: fix in `internal/repopath/repopath.go` or the offending caller. Any change to existing on-disk layout needs a maintainer decision (migration or fallback lookup); stop and report instead of changing layout silently.

### Test plan
- Command: `go test ./internal/servecmd/ ./internal/repopath/ ./internal/state/ ./internal/controller/` (full: `make test`).
- Cases: `TestRepoPathsDistinct`, `TestRepoPathsCaseFold`, `TestServeLocksPerRepo` as above.
- Failure case: an invalid repo (`a/b/c`, `../x`) makes `repoPaths` return an error, not a path.
- Edge: dry-run and live state dirs differ for the same repo, while the lock is the same in both modes (documented in `LockPath`).

### Risk
Low. Tests only, no production change expected. No protected paths touched. Risk rises to medium only if a collision is found and a layout fix is needed.

### Size
M (about 150 changed lines, one new test file): `internal/servecmd/isolation_test.go` (plus an optional `_unix_test.go` split).
