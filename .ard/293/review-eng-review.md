---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/roles/security_review_test.go"
    line: 71
    severity: info
    message: |
      The prompt size cap was raised from 16 KiB to 18 KiB to fit the new section (the prompt is about 16.8 KB). This is a reasonable and explained change; no action needed.
---
The change adds a `## Repo context` section to the 11 starter prompts as plan.md describes. Each prompt names only its readers-table files and states the absent rule, precedence and conflict handling. The four reviewer prompts carry the PR-head rule. The docs update and the new table-driven test match the issue. Approved.

## Checks
- Lint: `make lint` was not completed (the first combined test and lint run timed out). `gofmt -l internal/roles` is clean for the changed Go files.
- Tests: `go test -short -run 'Context|Template|Prompt' ./internal/roles/` passes. The new tests exercise every added prompt line through substring checks.
- Diff coverage: not applicable; the change adds only prompt text, docs and a test.
