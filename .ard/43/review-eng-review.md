---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change adds the starter `reviewers/security-review` role, its config override and docs, as the plan describes. The prompt's example reports are parsed by tests and the embedded-file lists are updated. Approved.

## Checks
- Lint: not run. The change touches no Go code except tests and a comment, and `go test` compiles them.
- Tests: `go test ./internal/roles ./internal/initcmd` passes.
- Diff coverage: not run. The only new code is test code, and the new tests execute the embedded prompt and config directly.
