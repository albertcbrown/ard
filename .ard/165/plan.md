# Plan for #165

## Implementation

### Steps
1. New `internal/gitx/version.go`: add `var MinVersion = Version{2, 47, 0}`, `type Version struct{ Major, Minor, Patch int }` with `String()` and `Less(Version) bool`, and `func ParseVersion(s string) (Version, error)`. It parses the output of `git --version`, e.g. `git version 2.50.1 (Apple Git-155)` or `git version 2.39.5`. It takes the first three dot-separated numeric fields and tolerates a missing patch and vendor suffixes. Reason: one place owns the minimum.
2. Same file: `func (g *Git) Version(ctx context.Context) (Version, error)`. It runs `git --version` through the existing unexported `run` (`internal/gitx/gitx.go:55`) with an empty dir, then calls `ParseVersion`. Also `func (g *Git) CheckVersion(ctx context.Context) error`. It returns nil when the version is >= `MinVersion`. Otherwise it returns an error like `git 2.39.5 is too old: ard needs git 2.47.0 or newer (the committer's fetches fail on older versions)`. It wraps a failure to run or parse git with a clear message.
3. `internal/servecmd/serve.go`: right after `resolveRepo` succeeds (about line 72) and before `state.CheckLegacy` and `AcquireLeader`, call `gitx.New("").CheckVersion(ctx)`. On error return `&cli.ExitError{Code: 1, Err: fmt.Errorf("ard serve: %w", err)}`, matching the neighbouring checks. Reason: fail before any state, lock or clone work.
4. `internal/configcmd/config.go` `Check`: call the same check first, before `config.RepoRoot`, and return `fmt.Errorf("ard config check: %w", err)`. Do it unconditionally, so the check is the same with or without `--ref`.
5. Docs: add one sentence to the local-executor/clone paragraph in `docs/proposal.md` (the line starting "Local-executor details") saying that `ard serve` and `ard config check` refuse to run on git older than 2.47.0. Keep the edit to one sentence. Add a short "Requirements" note to `README.md` only if it already has a setup section. Otherwise skip it.
6. Leave the optional "make the fetch work on 2.39" part as a separate follow-up issue.

### Test plan
Command: `go test ./internal/gitx/... ./internal/servecmd/... ./internal/configcmd/...`, then `go test ./...`.
- `internal/gitx/version_test.go`, table test `TestParseVersion`:
  - `git version 2.39.5` -> {2,39,5}.
  - `git version 2.50.1 (Apple Git-155)` -> {2,50,1}.
  - `git version 2.47` -> {2,47,0}.
  - `git version 2.48.0.rc1` -> {2,48,0}.
  - `git version 2.51.0.windows.1` -> {2,51,0}.
  - Failures: empty string, `garbage`, `git version x.y`.
- `TestVersionLess`: compare across major, minor and patch boundaries, and equal versions.
- `TestCheckVersion`: write a shim shell script into `t.TempDir()` that echoes a chosen version. The script should use `exec.LookPath("sh")` or a `#!/bin/sh` line, as `internal/roles/cve_test.go` does. Build the Git with `gitx.New(shim)`. 2.39.5 gives an error containing both `2.39.5` and `2.47.0`. 2.47.0 and 2.56.1 give nil. A shim that prints junk gives a parse error. A missing binary gives an error.
- `internal/servecmd/serve_test.go` (follow the existing harness): the old-git case is hard to inject because the call uses `gitx.New("")`. To make it testable, pass the binary through a package var such as `var gitBin = ""` (new, in servecmd). The test sets it to a shim reporting 2.39.5 and asserts the `ard serve:` error, exit code 1, and that no state directory was created. Do the same for configcmd with `TestCheckRefusesOldGit`.
- Edge: a patch-level difference below the minimum (2.46.9) is rejected.
- Edge: existing tests must pass on the dev machine's git (2.50 or newer).

### Risk
Low. The change is additive and fails early. Risks:
- The minimum is a conservative guess. Only 2.47.3 (pass) and 2.39.5 (fail) were observed, and no git between them is available here to bisect. A too-high minimum blocks operators needlessly. A maintainer should confirm, or run `go test ./internal/committer ./internal/gitx` against intermediate gits and lower `MinVersion`.
- Hosts with a vendor git string (Apple, Windows) rely on the tolerant parser.
- `ard config check` becomes dependent on git being runnable. It already uses git for `--ref`.
- No protected paths in `merge.protected_paths` are touched. No migrations or concurrency. The public CLI behaviour changes only for old git.

### Size
S to M, about 150 changed lines, mostly tests. Files:
- `internal/gitx/version.go` (new)
- `internal/gitx/version_test.go` (new)
- `internal/servecmd/serve.go`
- `internal/servecmd/serve_test.go`
- `internal/configcmd/config.go`
- `internal/configcmd/config_test.go`
- `docs/proposal.md`
- `README.md` (optional)
