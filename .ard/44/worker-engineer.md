---
role: engineer
status: done
attempt: 1
---
Adds a starter `reviewers/license-review` role to the embedded roles: a prompt that blocks any added or upgraded dependency whose license is outside an owner-controlled allowlist, a default permissive allowlist, and a starter config override that runs it only when a manifest or lockfile changes.

## Changes
- `internal/roles/reviewers/license-review/`: `prompt.txt` (allowlist lookup from the sidecar, else the role directory; go-licenses and `go mod download` aids; npm lockfile license field; verdict rules; five parseable examples) and `allowlist.txt` (permissive SPDX ids only).
- `internal/roles/config.yaml`: Sonnet 5.5 override with `when.paths` for go.mod, go.sum and the npm manifests and lockfiles.
- `internal/roles/roles.go`, `roles_test.go`, `internal/initcmd/initcmd_test.go`: doc comment and expected starter file lists.
- `internal/roles/eng_review_test.go`: the reviewer-list check now tolerates sibling reviewers.
- `internal/roles/license_review_test.go` (new): role loading, prompt shape, example parsing and labels, allowlist format.
- `internal/roles/license_review_integration_test.go` (new, `integration` tag): go-licenses blocks a non-allowlisted MIT module and passes when listed.
- `docs/proposal.md`, `CLAUDE.md`: describe the role (CLAUDE.md is a protected path, so the PR waits for human approval).

## Testing
`make test` passes. An earlier run failed `TestLicenseReviewPromptShape` because the prompt lacked the literal plan.md path; fixed in the prompt. The integration test compiles (`go vet -tags integration`) but was not run: go-licenses is not installed here.

## Open issues
- `TestIntegrationAllowlistCoversArd` (needs network) was not written.
- The proposal's sidecar/step-3 allowlist location text was covered in the new reviewer paragraph rather than edited elsewhere.
