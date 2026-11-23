# Plan for #48

## Implementation

The issue body's "Decisions", "Engineer view" and "Tests" sections, with the owner's resolved open questions (fail closed on missing baseline, Go only, nolint findings, protect `.golangci.*`, collapse identical messages, two-level severity), are the specification. Follow them exactly. This section only orders the work.

### Steps

1. New `internal/roles/scanners/lint/normalize.jq`: pure jq (1.6+) with `rel`, `rule_and_msg`, `norm`, `fp` (512-byte fit), `fence`/`code`, `sev`, candidates from `$gl[].report.Issues` (typecheck is `error(...)`) and `$nolint[]`, collapse by fingerprint, baseline parse, and `$mode` of `findings`, `baseline` or `fingerprints`. Build first so golden tests can drive it. Pattern: `internal/roles/scanners/cve/merge.jq`.
2. New `internal/roles/scanners/lint/scan` (POSIX sh, mode 0755): follow the "Engineer view" steps 1 to 8. Model it on `internal/roles/scanners/cve/scan` (the `need`, temp dir, trap, cache env and find-prune code). Forced golangci-lint flags exactly as in decision 2. Stdout only as the last action. Exit codes 0, 1 and 2. Set the mode with `chmod 0755` and check `git ls-files -s` shows 100755.
3. New `internal/roles/scanners/lint/Dockerfile`: header with the build and baseline commands, bump rule and verification date; `GO_IMAGE` digest copied from `scanners/cve/Dockerfile`; golangci stage with `go install ...@v2.15.0` and `CGO_ENABLED=0`; jq 1.8.2 stage with the per-arch SHA-256 ARGs copied from cve's Dockerfile; `ENV GOTOOLCHAIN=local TMPDIR=/var/tmp`. Pins cannot be checked without network, so mark the header "verification pending human".
4. New `internal/roles/testdata/lint/` cases: synthetic cases hand-written (`empty`, `digits_normalized`, `code_prefixes`, `baseline_filters`, `baseline_mode`, `fingerprints_mode`, `bad_baseline_line`, `typecheck`, `path_outside_root`, `control_char_path`, `long_message`, `untrusted_text`, `severity_table`, `nolint_directives`, `many_lines`). Add `regen.sh` for the real cases (`basic`, `nested_module`, `repo_config_new_only`), modelled on `testdata/cve/regen.sh`. The worker has no Docker and cannot run it, so the worker hand-builds the real cases' `gl.jsonl`, `nolint.jsonl` and `want.jsonl` from the documented golangci-lint JSON shape and flags them for human regeneration.
5. New `internal/roles/lint_test.go`: `TestLintNormalizeGolden`, `TestLintFixtureCasesListed`, `TestLintWantDecodes` (uses `scanner.DecodeFindings`, `scanner.MaxTitleRunes`). Reuse `needTools` from `cve_test.go`.
6. New `internal/roles/lint_scan_test.go`: `TestLintScanStubbed` with stub `golangci-lint` and `go` on a temp PATH (cases in the issue).
7. `internal/roles/config.yaml`: add the `scanners/lint` override with a one-line comment, and the four `**/.golangci.*` entries in `protected_paths` under a comment.
8. `internal/roles/roles.go`: update the doc comment role list only.
9. `internal/roles/roles_test.go`: add the three lint files to the `TestFSContents` list (keep sorted), add `scanners/lint/scan` to `TestScanIsExecutableOnDisk`, add `TestEmbeddedLintRoleLoadsAsScanner`, and a `config.Protected` check on `.golangci.yml`.
10. `internal/initcmd/body.go`: add the fixed-text baseline bullet under "Before you merge" when ops include `.ard/scanners/lint/scan`. `internal/initcmd/initcmd_test.go`: add the three files to `starterTree`, assert 100755 for lint `scan`, the override, the `ard:scanner:lint` label and the bullet. Check the other assertions at about lines 201 and 239 still pass.
11. `Makefile`: add `lint-scanner-fixtures` (to `.PHONY` too), mirroring `cve-fixtures`.
12. Integration tests under `//go:build integration`: `internal/roles/lint_integration_test.go` and `lint_docker_integration_test.go` (reuse `scanRunArgs`, generalise its doc comment in `cve_docker_integration_test.go`). These are compiled only with the tag, so keep them vet-clean (`go vet -tags integration ./internal/roles/`).
13. Docs: `docs/proposal.md` (Scanners table row, new "The lint scanner" paragraph, `ard init` paragraph and protected-path defaults) and `CLAUDE.md` Layout lines (both `internal/roles/` lines add `scanners/lint`).

### Test plan

- Command: `make lint test` (`go vet`, gofmt check, `go test ./...`). Also `go vet -tags integration ./internal/roles/`.
- Golden cases: the named cases in step 4. Each runs jq as `scan` does with `/R` and `/L` roots and compares with `want.jsonl`, `want.txt`, or checks that stderr contains `want.err` with a non-zero exit.
- Stubbed scan cases: success, argv forced flags, golangci exit 3 (exit 1, empty stdout), Go-version error, typecheck issue, golangci-lint missing (exit 2), no baseline (exit 1, message mentions `scan --baseline`), `--baseline` without a baseline (exit 0), bad argument (exit 1), no go.mod with a nolint file, read-only repo (no files written).
- Edge cases: more than 20 lines ("and N more"), a 512-byte fingerprint, a 200-rune title, hostile text (fences, comment delimiters, `@name`, backticks), CRLF baseline.
- Not runnable by the worker: Docker integration, `make lint-scanner-fixtures`, and the Dockerfile pin checks. A human runs them before merge.

### Risk

Medium. The new code is isolated in a new role directory. The risks are: wrong flags or fingerprints that cause mass autoclose (covered by the argv test); untrusted text rendered into issue bodies (covered by `untrusted_text`); hand-built real fixtures that may differ from true golangci-lint output; and unverified Dockerfile pins. The starter `protected_paths` change and CLAUDE.md are edited. Protected paths touched: `CLAUDE.md` (in `merge.protected_paths`), so a human approval is needed. The new Dockerfile is in a subdirectory, and the root `Dockerfile` entry does not match it. Nothing under `.ard/` or `.github/` is changed.

### Size

L (about 900 to 1,500 changed lines with fixtures; under the 2,000-line default block limit). Files touched: `internal/roles/scanners/lint/{scan,normalize.jq,Dockerfile}`, `internal/roles/testdata/lint/**`, `internal/roles/{lint_test.go,lint_scan_test.go,lint_integration_test.go,lint_docker_integration_test.go,cve_docker_integration_test.go,roles.go,roles_test.go,config.yaml}`, `internal/initcmd/{body.go,initcmd_test.go}`, `Makefile`, `docs/proposal.md`, `CLAUDE.md`.
