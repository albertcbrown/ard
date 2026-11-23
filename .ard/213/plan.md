# Plan for #213

## Implementation

### Steps

1. **New `internal/roles/scanners/missing-tests/scan`** (POSIX sh, mode 0755, header comment and structure copied from `scanners/docs/scan` and `scanners/cve/scan`). It finds its siblings from `$0` (`dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)`). `need go`; `mktemp -d "${TMPDIR:-/tmp}/ard-missing-tests.XXXXXX"` with the same `cleanup` and traps; the same GOCACHE/GOMODCACHE/GOPATH redirect; `GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly`, `CGO_ENABLED=0`.
   - Build the helper from the temp dir: `(cd -- "$tmp" && go build -o "$tmp/missingtests" "$dir/missingtests.go")`.
   - Find Go modules like cve does (`find . \( -name .git -o -name vendor -o -name node_modules -o -name testdata \) -prune -o -name go.mod -type f -print`, sorted with `LC_ALL=C`). For each module dir `mdir` (with `prefix` `.` for the root, else the repo-relative dir): read `module` from `go list -m`, run `go list -json ./...` into `$tmp/pkgs-N.json`, and, unless a human-owned `no-coverage` file sits next to `scan`, run `go test -count=1 -covermode=set -coverpkg=./... -coverprofile=$tmp/cover-N.out ./...` (stdout to stderr). Then call the helper with `-module -prefix -packages -go "$(go env GOVERSION)"` and `-profile` or `-no-coverage`, plus `-ignore "$dir/ignore"` when that human-owned file exists. This is the same argument set `mtMeasure` in `internal/roles/missing_tests_test.go` already uses.
   - A failing `go list`, `go test` (build failure or test failure) or helper exits 1 with `die "missing-tests: ..."` naming the module. A failing test suite means no trustworthy coverage, and autoclose must not close issues on partial data. Missing `go` exits 2. Each module's output goes to `$tmp/out-N.jsonl`; stdout gets only the final `cat` of all of them, as the last action. Log the Go version, one line per module and the final count to stderr.
   - No Go modules: log `no Go modules` and succeed with no output.
2. **New `internal/roles/scanners/missing-tests/Dockerfile`**, modelled on `scanners/docs/Dockerfile`: `FROM golang:1.27.4-trixie@sha256:f18c3d73a184fb0582828510722e723b63a93f7188d11dd223d9be6f6880e65d`, `ENV GOTOOLCHAIN=local`, `ENV TMPDIR=/var/tmp`, with a header comment on pinning and how to run. It does not copy `scan` or `missingtests.go`, because the executor mounts the role dir. Note in the comment that the scan runs the repo's tests, so the job needs egress to the module proxy.
3. **`internal/roles/roles.go`**: add `scanners/missing-tests` to the `//go:embed` line (`config.yaml reviewers scanners/cve scanners/docs scanners/error-codes scanners/missing-tests scanners/trufflehog triage workers`) and to the doc comment list. Check that `TestExecutableNames`' rule (names such as `scan`) still holds for the new file.
4. **`internal/roles/config.yaml`**: add `scanners/missing-tests: { every: 24h, max_open: 5, group_size: 10, autoclose: true }` under `overrides:`, next to error-codes. `group_size` follows the proposal's "10 per package" table row; the helper already sets each finding's group, so confirm against `missingtests.go`'s `Group` handling before fixing the value.
5. **Starter lists** (they fail until step 3 lands):
   - `internal/roles/roles_test.go`: add `scanners/missing-tests/Dockerfile`, `scanners/missing-tests/missingtests.go` and `scanners/missing-tests/scan` to `want` in `TestFSContents` (sorted order), and `scanners/missing-tests/scan` to `TestScanIsExecutableOnDisk`.
   - Add `TestEmbeddedMissingTestsRoleLoadsAsScanner`, modelled on the trufflehog one: kind scanner, `ScanPath .ard/scanners/missing-tests/scan`, `Dockerfile .ard/scanners/missing-tests/Dockerfile`, Autoclose, `Every 24h`, `MaxOpen 5`, the chosen group size.
   - `internal/initcmd/initcmd_test.go`: add the three `.ard/scanners/missing-tests/...` paths to `starterTree` (line 146) and the scan path to the PR-body list at line 201 if init's body lists each scan. Check `internal/initcmd` for any other hard-coded role list (grep for `trufflehog`).
6. **New tests in `internal/roles/missing_tests_test.go`**: `TestMissingTestsScanScript`. It copies each of the existing fixture directories to a temp repo (`copyTree`), runs `sh scanners/missing-tests/scan` through a helper like `runDocsScript` (a cwd of the repo, `HOME=/nonexistent`, `TMPDIR` a temp dir, empty Go cache variables), and compares stdout with `want.jsonl`. Fixtures with a `no-coverage` marker use a role dir copy with that file next to `scan`. The `ignore` fixture likewise copies the fixture's `ignore` file into a role dir copy. Then add the decode check (`scanner.DecodeFindings` on stdout) and the failure cases below. Fixtures' expected paths may need the fixture's `go.mod` prefix `.`; the files exist already under `testdata/missing-tests/*`, so no new fixtures are needed except for the cases below.
7. **New `internal/roles/missing_tests_integration_test.go`** with `//go:build integration`, copying `docs_docker_integration_test.go`: `TestMissingTestsIntegrationDocker` builds `scanners/missing-tests`, runs it with `scanRunArgs(tag, repo, roleDir, "TMPDIR=/var/tmp")` over the `basic` fixture and compares with its `want.jsonl`. Unlike docs, do not pass `--network none` if the fixture has dependencies; the fixtures are stdlib-only, so `--network none` still works.
8. **Docs.**
   - `docs/proposal.md`: the scanners table row already exists (line 215); update its engine text if needed to `go test` coverage plus a static rule (`sh` + Go helper). Add a "**The missing-tests scanner**" paragraph after the trufflehog or error-codes paragraph (rules, group, fingerprint, ignore and no-coverage files, fail-closed behaviour, not enabled on ard itself). Add `scanners/missing-tests` to the starter list in the `ard init` paragraph (line 442) and the `overrides` example (line 427).
   - `docs/layout.md` line 27: add `scanners/missing-tests` to the `internal/roles/` starter list. Use `docs/layout.md`, not `CLAUDE.md`: the layout moved there. Only if `CLAUDE.md` still names the starter roles does it need touching, and it is protected.
   - `docs/TODO.md` is a controller-owned list; leave it alone.

### Test plan

Command: `make test` (runs `go test ./...`); the Docker test runs with `go test -tags integration ./internal/roles -run MissingTests`.

- `TestMissingTestsScanScript` per existing fixture (basic, build_tags, ignore, implicit_methods, no_coverage, no_test_files, xtest): the script's stdout equals the `want.jsonl` and the exit code is 0.
- Decode: the output of every fixture passes `scanner.DecodeFindings`.
- Fixed order and determinism: two runs give identical output.
- Failure, failing test: a module whose test fails exits 1, stdout empty, stderr names the module.
- Failure, build error: a package that does not compile exits 1, stdout empty.
- Failure, missing tool: with `PATH` lacking `go`, exit 2 and `go not found`.
- Edge, repo with no `go.mod`: exit 0, no output.
- Edge, nested module (new tiny fixture, root plus `sub/go.mod`): the findings of both modules appear with correct repo-relative paths.
- Edge, read-only worktree and unwritable HOME: the script writes nothing in cwd (`HOME=/nonexistent`).
- Roles: `TestFSContents`, `TestScanIsExecutableOnDisk` and the new `TestEmbeddedMissingTestsRoleLoadsAsScanner` in `internal/roles/roles_test.go`; `starterTree` in `internal/initcmd/initcmd_test.go`.
- Integration (tag `integration`): the Docker run matches the `basic` fixture.

### Risk

Low to medium. The scan runs the target repository's own tests, so it executes repo code. That is why it must run only in the executor's container with no token. It is an existing pattern: the cve scanner already runs `go` over the repo. It changes the `ard init` starter tree (public output), so the init tests must move with it. It touches no protected path from `merge.protected_paths`: the new Dockerfile is under `internal/roles/`, and the protected `Dockerfile` entry matches only the root one. A reviewer may still check that. If `CLAUDE.md` is edited, a human must approve the merge. Turning the scanner on in this repo's own `.ard/` is out of scope. No migrations, no concurrency.

### Size

L-ish but well under the limit: about 350 to 600 changed lines (the scan script about 150, tests about 200, docs about 40, the rest small).

Files: `internal/roles/scanners/missing-tests/scan` (new), `internal/roles/scanners/missing-tests/Dockerfile` (new), `internal/roles/roles.go`, `internal/roles/config.yaml`, `internal/roles/roles_test.go`, `internal/roles/missing_tests_test.go`, `internal/roles/missing_tests_integration_test.go` (new), optional new fixture under `internal/roles/testdata/missing-tests/`, `internal/initcmd/initcmd_test.go`, `docs/proposal.md`, `docs/layout.md`. Size L.
