---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change adds a `## Repo context` section to the 11 starter prompts, updates docs/proposal.md and adds table-driven tests, as plan.md and the issue ask. No blocking findings. Approved.

## Checks
- Lint: `make lint` was not run. `gofmt -l` on the changed Go files reports nothing.
- Tests: `go test -run 'Context|Template|Prompt' ./internal/roles/` passes. The full package run timed out here, so it was not repeated in full.
- Diff coverage: not applicable; the change adds prompt text, docs and a test.

## Previous findings
- internal/roles/security_review_test.go: no longer applicable. It was an info note about raising the prompt size cap, which needs no action.
