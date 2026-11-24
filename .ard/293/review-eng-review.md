---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change adds a `## Repo context` section to the 11 starter prompts, updates docs/proposal.md and adds table-driven tests, as plan.md and the issue ask. The reader table, absent rule, precedence, conflict wording and reviewer PR-head rule all match. No blocking findings. Approved.

## Checks
- Lint: `make lint` was not run. `gofmt -l internal/roles` reports nothing on the changed Go files. It only complained about a parse error in an existing testdata fixture, which the change did not touch.
- Tests: `go test -run 'Context|Template|Prompt' ./internal/roles/` passes. The full package run timed out here, so it was not repeated in full.
- Diff coverage: not applicable; the change adds prompt text, docs and a test.

## Previous findings
- internal/roles/security_review_test.go: no longer applicable. It was an info note about raising the prompt size cap, which needs no action.
