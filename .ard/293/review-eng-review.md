---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change adds a `## Repo context` section to the 11 starter prompts, updates docs/proposal.md and adds a table-driven test, all as issue #293 asks. No blocking findings. Approved.

## Checks
- Lint: `make lint` was not run. `gofmt -l internal/roles` reports only a parse error in the untouched `testdata/docs/skips/src/_x.go`. It reports nothing for the changed Go files.
- Tests: `go test -run 'Context|Template|Prompt' ./internal/roles/` passes.
- Diff coverage: not applicable; the change adds prompt text, docs and a test.

## Previous findings
- internal/roles/security_review_test.go: no longer applicable. It was an info note about raising the prompt size cap, which needs no action.
