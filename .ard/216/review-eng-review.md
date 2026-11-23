---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/roles/trufflehog_test.go"
    line: 566
    severity: high
    blocking: true
    message: |
      The `allow` file's positive path is untested, though plan.md lists `TestTrufflehogAllow`. The worker's report confirms it was not written.
      The only allow test is the bad-entry failure. Nothing checks that a listed fingerprint is dropped, that comments and blanks are tolerated, or that a missing `allow` keeps every finding. A bug in `merge($allow)` (findings.jq:292) would silently suppress real leaked-credential findings, or never suppress false positives. Add a case with an `allow` file in a copied role dir that holds one fingerprint, a comment and a blank line. Expect that finding gone and the count check still passing.
  - file: "internal/roles/trufflehog_test.go"
    line: 683
    severity: medium
    blocking: false
    message: |
      plan.md's `TestTrufflehogExcludePaths` is missing. Nothing asserts the `-x` file holds `.git` plus the three shipped regexes, or that removing `exclude-paths` leaves `.git` only. The fake trufflehog does not record the `-x` file's contents, so extend it to copy that file. The same file is also missing the plan's separate Go golden recomputation of the fingerprints and the 60-location "and 10 more" and backtick-fence cases (`TestTrufflehogOutputPassesGuards`).
  - file: "internal/roles/trufflehog_test.go"
    line: 600
    severity: medium
    blocking: false
    message: |
      The canary check covers only `RawV2` and `Redacted`. plan.md also names `SecretParts`, `ExtraData`, `StructuredData`, `VerificationError` and the repository fields, plus a killed-mid-stream case. The projection in findings.jq is a whitelist, so the risk is low, but this is the leak guard for a public issue. Add the missing fields to `thResultLine`, and check that the temp dir holds no marker.
  - file: "internal/roles/scanners/trufflehog/scan"
    line: 429
    severity: info
    message: |
      The all-files-excluded case (chunks 0 in a non-empty tree) fails closed, as plan.md accepts. It makes a repository with only vendored files fail every run. This is a known edge case and needs no action.
---
The scanner, config, embed and `ard init` note match plan.md, and the scan fails closed. But the `allow` file's suppression path, which a plan test case names, has no test, so this needs changes. The exclude-paths and canary coverage gaps are smaller and do not block.

## Checks
- Lint: the repository's lint entry point was not run. I ran `go vet` on `./internal/roles` and `./internal/initcmd`, which found nothing on the changed files. `gofmt -l` flags only the pre-existing testdata file `internal/roles/testdata/docs/skips/src/_x.go`, which the change did not touch.
- Tests: `go test ./internal/roles/ ./internal/initcmd/` passes.
- Diff coverage: no Go coverage profile was taken, because the change adds almost no Go code. Reading the shell and jq: the `allow` filter's success path, and the exclude-paths contents and `-x` file, are never exercised.
- The Dockerfile build and the checksums were not verified (they need network access).

## Previous findings
- The previous review (approve) had no findings.
