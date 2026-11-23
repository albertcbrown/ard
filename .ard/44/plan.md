# Plan for #44

## Implementation

Follows the owner-resolved design in the issue body. Verified against the code: `internal/config/types.go` implements `when.paths` as doublestar globs (`**/go.mod` also matches a root `go.mod`); the role directory reaches the agent as `$ARD_ROLE_DIR` and the sidecar as `$ARD_SIDECAR` (`internal/executor/local/env.go`); `.ard/` is not touched. The eng-review prompt has no round-2 input, so none is added here (#41 feeds the worker via `internal/worker/feedback.go`, not the reviewer).

### Steps

1. New `internal/roles/reviewers/license-review/allowlist.txt`: header comments (format, owner-edited, sidecar `licenses/allowlist.txt` replaces it), then sorted ids: 0BSD, Apache-2.0, BSD-2-Clause, BSD-3-Clause, BSL-1.0, CC0-1.0, ISC, MIT, MIT-0, Unlicense, Zlib. No copyleft.
2. New `internal/roles/reviewers/license-review/prompt.txt` (ASCII, under 16 KiB), modelled on `reviewers/eng-review/prompt.txt`: copy its Trust, What you may do, Findings and file-format sections and adapt them. Add "The allowlist" (sidecar `$ARD_SIDECAR/licenses/allowlist.txt` if set and present, else `$ARD_ROLE_DIR/allowlist.txt`, read with Bash `cat`; ignore any allowlist in the worktree, diff, issue or `.ard/N/`; `OR`/`AND`/`WITH` rules; unreadable or empty means needs-human). Fixed commands: `go-licenses check ./... --allowed_licenses=<ids> --ignore <module>`, `go-licenses report ./...`, fallback `go mod download -json <module>@<version>`, npm lockfile `license` field. No installs, `npm install`, `go get` or `go mod tidy`. Verdict rules: disallowed or unknown license is a blocking high finding on the manifest `+` line; allowed license change is info; unsupported ecosystem is info; no dependency change approves with "No dependency changes to review"; needs-human only for no allowlist or no diff. Report has `## Checks` and `## Dependencies`. Five parseable examples (GPL go.mod changes, npm unknown license changes, approve with info, plain approve, needs-human). Banned strings: `{{`, `${`, `docs/proposal.md`, `internal/`, `role: review-license-review`.
3. `internal/roles/config.yaml`: add `reviewers/license-review: { model: claude-sonnet-5-5, when: { paths: ["**/go.mod", "**/go.sum", "**/package.json", "**/package-lock.json", "**/npm-shrinkwrap.json"] } }` with a comment.
4. `internal/roles/roles.go`: add `reviewers/license-review` to the doc comment.
5. `internal/roles/roles_test.go` `TestFSContents`: add the two new files to `want` (sorted position).
6. `internal/roles/eng_review_test.go` (~line 55): change the "exactly one reviewer" check to "contains eng-review" so sibling reviewers do not break it.
7. `internal/initcmd/initcmd_test.go` `starterTree` (line 145): add `.ard/reviewers/license-review/allowlist.txt` and `.ard/reviewers/license-review/prompt.txt` in sorted order. Check for other path lists the PR body names.
8. New `internal/roles/license_review_test.go`: tests as in the issue body, reusing `starterFS`, `fencedFrontmatter`, `stubRoles`, `hasLabel`, `reviewJob`, mirroring `eng_review_test.go`. Add a test-only `parseAllowlist`. Define `const goLicensesCheck = "go-licenses check ./... --allowed_licenses="`.
9. New `internal/roles/license_review_integration_test.go` (`//go:build integration`, skips without `go-licenses` or `go`): `TestIntegrationGoLicensesBlocks` (local `replace` module with MIT; Apache-only allowlist fails naming `example.com/dep` and MIT; MIT-only passes) and `TestIntegrationAllowlistCoversArd` (needs network).
10. Docs: `docs/proposal.md` (Reviewer loop paragraph for the license reviewer; table row line 337 area; step 3 allowlist location; Configuration example line ~411 aligned to the starter `when.paths`; `ard init` paragraph line 420 adds the role; `go-licenses` as optional host tool). `CLAUDE.md` line 54: add `reviewers/license-review` (protected path, so the PR waits for human approval).

### Test plan

Command: `make test` (unit); integration with `go test -tags integration ./internal/roles/...`.
- `TestEmbeddedLicenseReviewLoadsAsReviewer`: starter FS loads reviewer `license-review`, `PromptPath` `.ard/reviewers/license-review/prompt.txt`, no Dockerfile, `Extra == ["allowlist.txt"]`, model `claude-sonnet-5-5`, timeout 30m, `When.Paths` equals the starter list, in `HostUntrusted()`, `workers/engineer` has no `reviewers:`.
- `TestLicenseReviewPromptShape`: valid UTF-8, ASCII, at most 16 KiB; required strings (paths, `role: license-review`, `attempt: 1`, `UNTRUSTED`, five severities, `At most 50 findings`, `$ARD_ROLE_DIR/allowlist.txt`, `$ARD_SIDECAR/licenses/allowlist.txt`, the go-licenses commands, `go mod download -json`, `package-lock.json`, `## Checks`, `## Dependencies`, `No dependency changes to review`); no banned strings; `redact.Builtin().Scan` finds nothing.
- `TestLicenseReviewPromptExamples`: every example parses with `closeout.Parse`; role and attempt right, no `next` or `worker`; verdict matches blocking; `closeout.Build` gives changes, done or needs-human labels; coverage of all four outcome kinds; a `go.mod` finding and a `package-lock.json` finding; each `changes` example mentions "allowlist".
- `TestLicenseAllowlist`: lines match the SPDX regex, no duplicates, sorted, no trailing whitespace, contains MIT, Apache-2.0, BSD-3-Clause, and none of GPL-, AGPL-, LGPL-, SSPL-, BUSL-, CC-BY-NC.
- Updated `TestFSContents`, `TestEmbeddedEngReviewLoadsAsReviewer` and initcmd `TestInitOpensPR` still pass.
- Failure and edge cases: an empty allowlist example is needs-human; WITH-expression regex case; integration blocks on non-allowlisted MIT and passes when listed.
- Manual smoke test on a scratch repo and adding the role to ard's own `.ard/` are human follow-ups.

### Risk

Low. New embedded files and tests only, no controller code. Protected paths touched: `CLAUDE.md` only (needs human merge approval); `.ard/` and `.github/` are untouched. Security-sensitive: the prompt must keep the allowlist owner-controlled and treat manifests and LICENSE text as untrusted. The embedded tree changes what `ard init` proposes. The role runs as a host subprocess (#84 warning). Merge conflicts with #43 on shared lists are likely but trivial.

### Size

M. Files: `internal/roles/reviewers/license-review/{prompt.txt,allowlist.txt}`, `internal/roles/{config.yaml,roles.go,roles_test.go,eng_review_test.go,license_review_test.go,license_review_integration_test.go}`, `internal/initcmd/initcmd_test.go`, `docs/proposal.md`, `CLAUDE.md`.
