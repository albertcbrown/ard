# Plan for #49

## Implementation

### Steps
The design is the issue's own "Implementation plan" (resolved open questions: three sets registry/docs/sources, no-op without `codes.json`, not run on ard itself, severities high/medium/low). Follow it. Order keeps `make test` green after each step.

1. New `internal/roles/scanners/error-codes/errcodes.jq` (new): one program with `$mode` = `settings|classify|match|findings`. Validates `codes.json` (unknown key, wrong type, list >100, empty or empty-matching `pattern`, uncompilable regex are `error`), wraps `pattern` as `(?<![A-Za-z0-9_])(?:P)(?![A-Za-z0-9_])`, turns globs into anchored regexes, classifies paths to `r|d|s` (path in both registry and docs is an error), extracts codes (allowed set `[A-Za-z0-9_.:/+=-]{1,128}`, others become `{skip:"code"}`), and builds findings (kinds duplicated/high, unregistered/medium, undocumented/low; fingerprint `error-codes:<kind>:<code>`; keys ordered `fingerprint,title,severity,group,files,body`; sorted; `files` max 20; body fence with up to 50 locations, paths stripped of backtick, CR, LF, `<`, `>`; more than 5,000 findings is an error).
2. New `internal/roles/scanners/error-codes/scan` (new, mode 0755, POSIX sh, same header and contract as `scanners/cve/scan`): exit 0/1/2; find the role dir from `$0`; no `codes.json` logs "not configured" and exits 0; temp dir with traps; settings, `find` with prunes (`.git`, `.ard`, `vendor`, `node_modules`, `testdata`, `-type f -size -1025k`), classify, `grep -I -l` text filter (any stderr fails), die on empty registry or empty docs when set, `awk` skipping lines over 16 KiB, `match` pass, `findings` pass, summary to stderr, then `cat` of the output file as the last step. Every failure leaves stdout empty (fail closed, autoclose safety).
3. New `internal/roles/scanners/error-codes/Dockerfile` (new): same `GO_IMAGE` digest and jq 1.8.2 checksums as `scanners/cve/Dockerfile`; no Go tools; header comments say `scan` and `errcodes.jq` are mounted, not copied.
4. `internal/roles/config.yaml`: add `scanners/error-codes: { every: 24h, group_size: 20, autoclose: true }` to `overrides`. `max_open` keeps the default (5).
5. `internal/roles/roles.go`: update the doc comment on `FS` to list `scanners/error-codes`. The `//go:embed` line already covers `scanners`; `ExecutableNames` is unchanged.
6. `internal/roles/roles_test.go`: add the three files to `TestFSContents`; make `TestScanIsExecutableOnDisk` loop over both scan scripts; add `TestEmbeddedErrorCodesRoleLoadsAsScanner`.
7. `internal/initcmd/initcmd_test.go` line 145: add `.ard/scanners/error-codes/Dockerfile`, `errcodes.jq`, `scan` to `starterTree` in sorted position.
8. New `internal/roles/errcodes_test.go` and `internal/roles/errcodes_integration_test.go` (see Test plan).
9. Docs: `docs/proposal.md` (error-codes table row engine, a "The error-codes scanner" paragraph after the cve one, and add `scanners/error-codes` to the "`ard init` proposes the starter `.ard/`" sentence and its override list near line 403); `CLAUDE.md` (the `internal/roles/` layout line lists `scanners/error-codes`).

### Test plan
Command: `make test` (or `go test ./internal/roles/ ./internal/initcmd/ ./internal/config/`). Tests need `sh` and `jq`; skip via `needTools` when missing. Reuse cve_test.go helpers: hermetic PATH of symlinked tools (`jq find awk grep xargs sort mktemp rm cat tr dirname env`), role dir copied from `FS`, repo under `t.TempDir()`, `HOME=/nonexistent`, private `TMPDIR`. Every successful stdout must pass `scanner.DecodeFindings`; every failure must leave stdout empty.
- `TestScan` table (file `internal/roles/errcodes_test.go`):
  - no codes.json: exit 0, empty stdout, "not configured" on stderr.
  - clean repo: exit 0, empty.
  - unused-in-registry use: one `unregistered`, medium, group `unregistered codes`, files = use files.
  - duplicate in one file and across two files: one `duplicated`, high.
  - same code twice on one registry line: no finding.
  - `entry` set, non-matching line: not a definition.
  - missing from docs: `undocumented` low; none when `docs` unset.
  - word boundaries `XE1001`, `E10011`, `E1001_x`: no match.
  - `.ard/12/issue.md`, `vendor/`, `testdata/`, `node_modules/`, `.git/`: ignored.
  - `exclude`, `sources`, `dir/` prefix, `**` glob: applied.
  - registry/docs files not counted as uses.
  - symlink to outside file: ignored.
  - binary file, file over 1 MiB, line over 16 KiB: ignored, long line counted on stderr.
  - code with `@`, backtick or over 128 bytes: dropped and counted.
  - hostile path ``a`<!--x-->.go``: `files` keeps it, fence not broken.
  - invalid codes.json (bad JSON, unknown key, wrong type, empty pattern, empty-matching pattern, bad regex, registry matching nothing, docs matching nothing, file in both): exit 1, empty stdout, message names the cause.
  - 5,001 findings: exit 1, empty stdout.
  - reordered file creation / moved use: same fingerprints; identical content gives byte-identical output.
  - symlinked cwd: same output.
- `TestErrCodesJQGlob`: glob table via `--arg mode settings` and `classify`.
- `TestErrorCodesDockerfilePinned`: `FROM` has `@sha256:`; digest and jq checksums equal cve's.
- `TestEmbeddedErrorCodesRoleLoadsAsScanner` (roles_test.go): `ScanPath == ".ard/scanners/error-codes/scan"`, `Every == 24h`, `GroupSize == 20`, `MaxOpen == 5`, `Autoclose`, `Dockerfile` set, `Extra == ["errcodes.jq"]`.
- `errcodes_integration_test.go` (`//go:build integration`): build the Dockerfile, run with repo `/repo:ro`, role `/role:ro`, `--read-only`, noexec tmpfs, `--network none`, `--user 65534`; compare with the host golden output; skip without docker. Not run by a worker job (no Docker).
- Also run `go vet ./...` and the existing initcmd and config tests.

### Risk
Medium. No controller code changes, but the scanner has `autoclose`, so any case where it sees less than it should would wrongly close issues; hence the fail-closed rules and tests. Portability between BSD tools on macOS and GNU tools in the image is the main technical risk (word boundaries via jq regex, `find`/`xargs -0`). `CLAUDE.md` is in `merge.protected_paths`, so the PR waits for human approval. A role `Dockerfile` is added under `internal/roles/` (not `.ard/`); no file under `.ard/` or `.github/` changes. Human steps before merge: verify the base-image digest and run the integration test once. Expect trivial textual conflicts with other PRs adding starter roles (`TestFSContents`, `starterTree`, config.yaml, proposal line, CLAUDE.md layout line); land them one at a time.

### Size
M to L by the issue's own estimate (about 700 to 900 lines, over half tests). Files: `internal/roles/scanners/error-codes/{scan,errcodes.jq,Dockerfile}` (new), `internal/roles/errcodes_test.go` (new), `internal/roles/errcodes_integration_test.go` (new), `internal/roles/config.yaml`, `internal/roles/roles.go`, `internal/roles/roles_test.go`, `internal/initcmd/initcmd_test.go`, `docs/proposal.md`, `CLAUDE.md`.
