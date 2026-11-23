---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The prompt change implements the issue and plan.md: the lint entry point order, no substituted tools, the fallback note in `## Checks`, the Trust-rule case and the updated example. The test list was extended to match. Approved.

## Checks
- Lint: `make lint` (the repository's entry point; the change touches neither the Makefile nor CI) reported nothing.
- Tests: `go test ./internal/roles/` passes.
- Diff coverage: skipped; the change edits a prompt text file and one test string list, with no new code lines.
