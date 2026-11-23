---
role: engineer
status: done
attempt: 1
---
Adds a starter product-manager reviewer that checks a pull request against the issue's numbered acceptance criteria and scope, and wires it into the embedded starter config so `ard init` proposes it.

## Changes
- `internal/roles/reviewers/product-manager-review/prompt.txt` (new): criteria come from the plan's Requirements, else the issue body; each is marked met, not met or cannot verify; severities and report format follow eng-review; read-only Bash only; six examples.
- `internal/roles/config.yaml`: override for `reviewers/product-manager-review` with `when: {triage: [product-manager]}` and a commented `paths` example.
- `internal/roles/roles.go`: doc comment lists the new role.
- `internal/roles/pm_review_test.go` (new): loading, `when` selection, dependency on the PM triage role, prompt shape, example blocks, contract traps.
- `roles_test.go`, `security_review_test.go`, `internal/initcmd/initcmd_test.go`: starter file, reviewer, PR-body path and label lists include the new role.
- `docs/proposal.md`: new paragraph, reviewer table row, init paragraph and example overrides.

## Testing
Ran `go test ./internal/roles ./internal/initcmd ./internal/config ./internal/labels/...`: all pass. `make test` was not run. The manual smoke test (a sample PR with an unmet criterion) is not done.

## Open issues
- The optional eng-review test helper extraction was skipped; the eng-review checks were copied instead.
- Enabling the role in ard's own `.ard/` is a separate human change.
