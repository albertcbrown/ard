---
role: engineer
status: done
attempt: 1
---
Adds the standalone Go helper of the missing-tests scanner, with fixtures and table-driven tests, following the design on the parent issue. It is not wired into the starter tree, so `ard init` writes the same files as before.

## Changes
- `internal/roles/scanners/missing-tests/missingtests.go`: stdlib-only helper behind `//go:build ignore`. It reads `go list -json` output and a coverage profile (or `-no-coverage`), applies the severity table and the fixed implicit-method list, supports an `ignore` file, and prints findings sorted by fingerprint. Any error prints to stderr, exits 1 and writes nothing to stdout. A hidden `-max` flag lets tests exercise the finding limit.
- `internal/roles/roles.go`: the embed directive now names `scanners/cve` and `scanners/error-codes` instead of all of `scanners`, so the new directory is not embedded. The wiring change must add `scanners/missing-tests` back.
- `internal/roles/testdata/missing-tests/`: seven fixture modules (basic, xtest, implicit_methods, build_tags, no_test_files, ignore, no_coverage), each with a `want.jsonl`.
- `internal/roles/missing_tests_test.go`: `TestMissingTestsHelper`, `...HelperOutputDecodes`, `TestMissingTestsFingerprintStable`, `...HelperLimits`, `...HelperErrors` and `...HelperVets`.

## Testing
- `go test ./internal/roles -run TestMissingTests -count=1`: pass.
- `make test` and `make lint`: pass. The existing `TestInitOpensPR` and `TestFSContents` pass unchanged, so the starter tree is unchanged.
- Each `want.jsonl` was generated from the helper once and checked by hand against the severity table.

## Open issues
- The tests share the default Go build cache. A private cache per case made the suite take about 55 s, because each case recompiled the standard library.
- The design text came from the parent issue's recorded triage log, since the parent has no plan file in the repository.
- A group longer than 256 bytes is replaced by `go:sha256:<hex>`, because the scanner schema rejects longer groups. The design did not say what to do in that case.
