---
role: engineer
status: done
attempt: 1
---
Adds the standalone Go helper of the missing-tests scanner, with fixtures and table-driven tests, following the design on the parent issue. It is not wired into the starter tree, so `ard init` writes the same files as before.

## Changes
- `internal/roles/scanners/missing-tests/missingtests.go`: stdlib-only helper behind `//go:build ignore`. It reads `go list -json` output and a coverage profile (or `-no-coverage`), applies the severity table and the fixed implicit-method list, supports an `ignore` file, and prints findings sorted by fingerprint. Any error prints to stderr, exits 1 and writes nothing to stdout. A hidden `-max` flag lets tests exercise the finding limit.
- `internal/roles/roles.go`: the embed directive names `scanners/cve`, `scanners/docs` and `scanners/error-codes` instead of all of `scanners`, so the new directory is not embedded. Wiring must add `scanners/missing-tests` back.
- `internal/roles/testdata/missing-tests/`: seven fixture modules (basic, xtest, implicit_methods, build_tags, no_test_files, ignore, no_coverage), each with a `want.jsonl`.
- `internal/roles/missing_tests_test.go`: `TestMissingTestsHelper`, `...HelperOutputDecodes`, `...FingerprintStable`, `...HelperLimits`, `...HelperErrors`, `...HelperVets`.

## Testing
- `go test ./internal/roles -run TestMissingTests -count=1`: pass.
- `make test`: pass, including `TestInitOpensPR` and `TestFSContents` unchanged, so the starter tree is unchanged.
- `make lint`: `go vet ./...` and `gofmt -l .` report only a syntax error in the existing fixture `internal/roles/testdata/docs/skips/src/_x.go`, which this change does not touch. `go vet ./internal/roles` passes and the new files are gofmt-clean.
- Each `want.jsonl` was generated from the helper once and checked by hand against the severity table.

## Open issues
- The plan's embed line omitted `scanners/docs`, which has been added to the repository since. It is kept in the directive so the starter tree does not lose it.
- A group longer than 256 bytes is replaced by `go:sha256:<hex>`, because the scanner schema rejects longer groups. The design did not say what to do then.
- The tests use the default Go build cache; the suite takes about 8 s warm.
- The `build_tags` fixture expects empty output, so it only proves that tagged files add no findings.
