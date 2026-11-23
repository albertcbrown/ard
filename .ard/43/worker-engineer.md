---
role: engineer
status: done
attempt: 1
---
Adds the starter reviewer role `reviewers/security-review`, embedded for `ard init`, which looks for injection, authz gaps, secrets and unsafe dependencies in a pull request and runs gitleaks, gosec and semgrep (local rules only) when they are installed.

## Changes
- `internal/roles/reviewers/security-review/prompt.txt`: the prompt from the plan (15,797 bytes, ASCII).
- `internal/roles/config.yaml`: override `reviewers/security-review` with Opus 5.5, no `when:`.
- `internal/roles/roles.go`: doc comment lists the new role.
- `internal/roles/security_review_test.go`: load, prompt shape, example and role-trap tests.
- `internal/roles/roles_test.go`, `internal/initcmd/initcmd_test.go`: starter file, PR body and label lists updated.
- `CLAUDE.md`, `docs/proposal.md`: role list, reviewer paragraph, table row and init sentence.

## Testing
`go test -short ./internal/roles ./internal/initcmd` passes. I did not run `make lint test` in full. The paid-model smoke run is the owner's.

## Open issues
- The plan was written before license-review landed on main, so the load test expects reviewers `eng-review,license-review,security-review`. `eng_review_test.go` already checks only that eng-review is present, so I left it unchanged.
- `CLAUDE.md` changed; it needs human approval.
