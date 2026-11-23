# Plan for #212

## Implementation

Design source: #50's implementation plan ("Finding definition", "`missingtests.go`", "Tests", and its resolved open questions). Follow it for the helper's rules, flags, fingerprint, title, body and output. This section only fixes the order and the parts specific to part 1.

### Steps

1. **Keep the helper out of the starter tree.** In `internal/roles/roles.go`, change the embed directive from `//go:embed config.yaml reviewers scanners triage workers` to `//go:embed config.yaml reviewers scanners/cve scanners/error-codes triage workers`. Reason: `scanners` embeds the whole directory, so a new `scanners/missing-tests/missingtests.go` would be written by `ard init` (`internal/initcmd`) and would break `TestInitOpensPR`'s `starterTree` check and `TestFSContents`. Add a one-line comment that part 2 (wiring) re-adds `scanners/missing-tests`. Do not edit `internal/roles/config.yaml`, `initcmd_test.go` or `roles_test.go`. `go build ./...` and `go test ./internal/roles ./internal/initcmd` must still pass at this point.
2. **New `internal/roles/scanners/missing-tests/missingtests.go`**: `//go:build ignore`, `package main`, stdlib only, with a header comment saying it is built by `scan` (part 2) and that `go vet ./...` skips it. Implement, as specified in #50:
   - flags `-module -prefix -packages -go`, exactly one of `-profile`/`-no-coverage`, optional `-ignore`, and a hidden `-max` (default `maxFindings = 10000`) so the limit test does not need 10,000 functions;
   - types `listedPackage`, `block`, `funcInfo`, `finding` (field order fingerprint, title, severity, group, files, body), `testRefs`, `ignoreRule`;
   - `readPackages`, `readProfile` (OR-merge duplicate blocks; error on a malformed line), `collectFuncs` (skip `main`, `init`, `_`, empty bodies, `ast.IsGenerated` files; parse error is an error), `collectTestRefs` and `testRefs.named`, `coverage`, `readIgnore` (error names the line) and `ignoreRule.match`, `decide` (the severity table, with `var implicitMethods`), `makeFinding` (fingerprint `missing-tests:go:<import path>:<symbol>`, sha256 form when over 512 bytes; title cut to 200 runes with `…`; body under 1,900 chars; group `go:<import path>`);
   - output buffered, sorted by fingerprint, one JSON object per line; on any error print `missing-tests: <what>` to stderr, exit 1, nothing on stdout.
3. **Fixtures under `internal/roles/testdata/missing-tests/<case>/`**: each case is a module (`go.mod` with `module example.com/mt`, `go 1.22`, no dependencies), `want.jsonl`, and optional `ignore` / `no-coverage`. Cases: `basic`, `xtest`, `implicit_methods`, `build_tags`, `no_test_files`, `ignore`, `no_coverage` (contents as in #50's table). Keep fixtures small and `gofmt`-clean (`make lint` runs `gofmt -l .` over `testdata`). Fixture Go files must not break `go vet ./...` or `go test ./...` of ard itself: `testdata` and the nested `go.mod` already exclude them. Generate each `want.jsonl` from the helper once, then review it by hand against the decision table.
4. **New `internal/roles/missing_tests_test.go`** (package `roles`): skip when `go` is not on `PATH`. Reuse `needTools` from `errcodes_test.go`. Add `buildHelper(t)` (`sync.Once`, builds `scanners/missing-tests/missingtests.go` from disk with `CGO_ENABLED=0 go build -o`) and a runner that executes `go list -json ./...` and `go test -count=1 -covermode=set -coverpkg=./... -coverprofile` in a fixture copy, then the helper, with env `GOWORK=off GOTOOLCHAIN=local GOPROXY=off GOFLAGS=` and a temp `GOCACHE`. Copy fixtures to a temp dir first so nothing is written under `testdata`. Do not add `TestMain` if one exists; check first with `grep -n TestMain internal/roles/*_test.go`, and clean the temp dir with `t.Cleanup` otherwise.
   Tests, all table-driven and named `TestMissingTestsHelper*` (see Test plan).

### Test plan

Command: `go test ./internal/roles -run 'TestMissingTests' -count=1`, then the full `make test` and `make lint`.

- `TestMissingTestsHelper` (table over the seven fixture cases): runs the real toolchain, then compares stdout to `want.jsonl` byte for byte. File: `missing_tests_test.go`.
- `TestMissingTestsHelperOutputDecodes`: every `want.jsonl` passes `scanner.DecodeFindings` and fingerprints are unique.
- `TestMissingTestsFingerprintStable`: in a generated temp module, move a function to another file in the same package and change its body; the fingerprint is unchanged. Also check that the signal (uncovered vs untested) does not change the fingerprint.
- `TestMissingTestsHelperLimits`: a 600-byte symbol gives the `missing-tests:go:sha256:` fingerprint; a long package path yields a title of at most 200 runes; `-max 2` with three findings fails with "too many findings".
- `TestMissingTestsHelperErrors` (table; each case expects exit 1 and empty stdout): missing `-packages`; both `-profile` and `-no-coverage`; neither; malformed profile line; Go file with a syntax error; malformed `ignore` line (stderr names the line number).
- `TestMissingTestsHelperVets`: runs `go vet scanners/missing-tests/missingtests.go` (explicit file, because `./...` skips ignore-tagged files). Also checks `gofmt -l` on the file is empty.
- Guard for done-when item 2: no new test needed; the existing `TestInitOpensPR` and `TestFSContents` must pass unchanged, which proves the starter tree is unchanged.

Edge cases covered by fixtures: generic and pointer receivers, method on an unexported type, generated file, empty body, `//go:build integration` file, `_windows.go` file, package with no tests, external `p_test` package with import alias, covered implicit methods (`Error`, `String`) exempt and uncovered ones `medium`.

### Risk

Low. All new files are test-only or `//go:build ignore`, and nothing in `internal/scanner`, `internal/config` or `internal/executor` changes. No protected path from `merge.protected_paths` is touched (`internal/roles/` is not protected; `.ard/`, `CLAUDE.md`, `deploy/` and `Dockerfile` are not edited). The one shared-code change is the embed directive in `roles.go`; part 2 must revert it, so note that in the comment. Fixture tests run `go test` in temp modules: they need no network (`GOPROXY=off`, no dependencies) but are slowish, and the Go version in the fixtures' `go.mod` must not exceed the local toolchain (`GOTOOLCHAIN=local`).

### Size

L (about 700 to 900 changed lines, mostly the helper at roughly 450 and the tests). If the helper plus tests passes about 900 lines, drop the `no_test_files` and `build_tags` fixtures into a follow-up rather than exceed the worker's cap.

Files: `internal/roles/roles.go` (changed); new `internal/roles/scanners/missing-tests/missingtests.go`, `internal/roles/missing_tests_test.go`, `internal/roles/testdata/missing-tests/**`.
