# Plan for #51

## Implementation

Scope: PR A only, the deterministic `docs` scanner. PR B (`docs-drift`, LLM) is blocked on the LLM scanner engine (#204) and is out of scope. Owner decisions on the open questions are recorded in the issue (two roles; every exported identifier in non-main packages; methods reported; `max_open: 3`, group = package dir, severity `low`).

### Steps

Each step leaves `make test` green.

1. **New** `internal/roles/scanners/docs/docscan.go` (`//go:build ignore`, `package main`, stdlib only). Flags `-root` (default `.`), `-exclude` (optional path). Unexported shape: `type finding struct` (JSON tags match `scanner.Finding`: fingerprint, title, severity, group, files, body), `type decl`, `loadExcludes(path) ([]string, error)`, `excluded(dir, pats) bool`, `walk(root, pats)`, `fileDecls(f *ast.File, rel string, fset *token.FileSet) []decl`, `build(pkgs) ([]finding, error)`. Rules, skip list, fingerprint/title/group/body formats and fail-closed behaviour are exactly as in the issue's "docscan.go" section. Key points:
   - skip testdata/vendor/node_modules/dot/underscore dirs, symlinks, `*_test.go`, `//go:build ignore`, `ast.IsGenerated`, `package main`;
   - report undocumented exported funcs, methods on exported receivers (strip `*` and type-parameter index exprs), types, const/var (one finding per undocumented block), and missing package comment;
   - merge duplicate `dir`+`symbol` across build-tagged files, listing only undocumented files;
   - hash fingerprint over 512 bytes, group over 256 bytes, cut title to 200 runes;
   - any parse/read error, invalid UTF-8 or control char, bad exclude glob, or more than 10,000 findings: exit 1 with nothing on stdout, reason on stderr;
   - output sorted by fingerprint, written once at the end.
2. **New** `internal/roles/scanners/docs/scan` (POSIX sh, git mode 100755), modelled on `internal/roles/scanners/cve/scan`: `set -eu`, siblings found from `$0`, `need go` (exit 2), `mktemp -d` with the same traps, the same GOCACHE/GOMODCACHE/GOPATH fallback loop, then `GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOFLAGS= CGO_ENABLED=0`. Build with cwd = `$tmp`, run `docscan -root . [-exclude "$dir/exclude"] > $tmp/out.jsonl`, log `go version` and the count to stderr, `cat` the output as the last action. Exit 0/1/2.
3. **New** `internal/roles/scanners/docs/Dockerfile`: same pinned base image line as cve's Dockerfile, plus `ENV GOTOOLCHAIN=local` and `ENV TMPDIR=/var/tmp`, no COPY, header comment with verification date and command.
4. `internal/roles/config.yaml`: add `scanners/docs: { every: 3d, max_open: 3, group_size: 10, autoclose: true }` to `overrides:`.
5. `internal/roles/roles.go`: add `scanners/docs` to the `FS` doc comment (line 10).
6. **New** fixtures `internal/roles/testdata/docs/<case>/{src/...,want.jsonl[,exclude]}` for the cases listed in the Test plan. No symlinks in fixtures.
7. **New** `internal/roles/docs_test.go` and `internal/roles/docs_docker_integration_test.go` (`//go:build integration`, modelled on `cve_docker_integration_test.go`), plus edits to `internal/roles/roles_test.go` and `internal/initcmd/initcmd_test.go` (see Test plan). Generate `want.jsonl` files from the built binary, then review them by hand against the rules.
8. `docs/proposal.md`: Scanners table (replace the `docs` row; add a `docs-drift` row marked "PR B, needs the LLM scanner engine"), a new "The docs scanner" paragraph after the cve paragraph (rules, skip list, fingerprint, no repo code run, no network, fails closed, `exclude` file and its autoclose consequence, Go only), and the Configuration section (starter list, starter-overrides sentence, example `overrides:` block).
9. `CLAUDE.md` line 54: add `scanners/docs` to the `internal/roles/` starter list. This is a protected path, so merge needs human approval.

Do not touch `.ard/config.yaml`, `.ard/scanners/`, `deploy/` or `.github/` (human follow-ups listed in the issue). Before writing `finding` handling, confirm `scanner.Finding` (`internal/scanner/finding.go`) has no `omitempty` tags: the strict decoder tolerates empty values, and the round-trip test confirms it.

### Test plan

Command: `make test` (or `go test ./internal/roles/... ./internal/initcmd/...` while iterating); `go vet internal/roles/scanners/docs/docscan.go` is run inside the tests because `go vet ./...` skips ignore-tagged files. Integration: `go test -tags integration ./internal/roles/` (skips without Docker).

`internal/roles/docs_test.go` (no network):
- `buildDocscan(t)` via `sync.Once`: `go build` with cwd = a temp dir and `GOTOOLCHAIN=local GOPROXY=off GOFLAGS= GOWORK=off`.
- `TestDocscanGolden` (table over `testdata/docs/<case>`): stdout equals `want.jsonl` byte for byte, then `scanner.DecodeFindings` succeeds. Cases: `all_documented` (empty), `funcs_methods` (pointer, value, generic receivers; unexported receiver skipped), `types_values`, `const_block` (one finding per block, unexported-only block none), `package_doc` (doc in one of two files; none; root `docs:pkg:.`), `skips`, `build_tags` (one undocumented file; both), `nested_module` (`sub/pkg`), `exclude` (`internal/` prefix, `cmd/*` glob, comments, blanks), `long_names` (title 200 runes, fingerprint over 512 bytes hashed, group over 256 bytes hashed), `no_go` (empty, exit 0).
- `TestDocscanFailsClosed`: syntax error, bad exclude glob `[`, unreadable file (chmod 000, skipped when root). Each: exit non-zero, empty stdout, stderr names the cause.
- `TestDocscanSkipsSymlinks`: symlinks to an outside file and a directory made at runtime yield no findings.
- `TestDocscanDeterministic`: two runs identical; fingerprints unique.
- `TestDocsScanScript`: `sh scanners/docs/scan` with cwd = fixture copy, `TMPDIR` = `t.TempDir()`, `HOME` unwritable: exit 0 and golden output; role dir copied with an `exclude` file applies the exclusion; a syntax-error tree exits 1 with empty stdout.
- `TestDocsScanOnThisRepo`: run on `../..`; decode succeeds, deterministic, no finding under `testdata/` or `.ard/`; no count assertion.

`internal/roles/roles_test.go`: `TestFSContents` list gains `scanners/docs/Dockerfile`, `scanners/docs/docscan.go`, `scanners/docs/scan`; `TestScanIsExecutableOnDisk` becomes table-driven over both `scan` files; new `TestEmbeddedDocsRoleLoadsAsScanner` (`ScanPath == ".ard/scanners/docs/scan"`, `PromptPath == ""`, `Autoclose`, `Every == 72h`, `MaxOpen == 3`, `GroupSize == 10`, `Extra == [docscan.go]`, Dockerfile set, `ReadsUntrusted() == false`).

`internal/initcmd/initcmd_test.go`: `starterTree` (line 145) gains the three docs files; `.ard/scanners/docs/scan` committed as `100755`; `ard:scanner:docs` in the synced label catalogue; starter config resolves the docs override. Check the other tests that list starter paths (lines 193, 224, 340) still hold.

Edge cases: repo with no Go files (valid empty run), duplicate symbols across build-tagged files, non-UTF-8 or control-character names, more than 10,000 findings.

### Risk

Medium-low. The change is additive: a new embedded role and tests, with no controller code touched. Reasons for care:
- The scanner autocloses, so a partial result would close real issues; fail-closed behaviour and single final write are the main safeguard and are tested.
- It parses untrusted repository files but never executes repo code and needs no network (`GOPROXY=off`, stdlib-only build).
- The first run on ard's own tree would file about 28 low issues; `max_open: 3` bounds this.
- Protected path: `CLAUDE.md` (in `merge.protected_paths`), so merge waits for a human. No other protected paths are touched. No migrations or public Go interfaces change; adding starter files changes `ard init` output.

### Size

L. Roughly 400 lines for `docscan.go`, 40 for `scan`, 500+ for tests, plus fixtures and docs.
Files: `internal/roles/scanners/docs/{scan,docscan.go,Dockerfile}`, `internal/roles/config.yaml`, `internal/roles/roles.go`, `internal/roles/docs_test.go`, `internal/roles/docs_docker_integration_test.go`, `internal/roles/roles_test.go`, `internal/roles/testdata/docs/**`, `internal/initcmd/initcmd_test.go`, `docs/proposal.md`, `CLAUDE.md`.
