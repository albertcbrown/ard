---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "docs/runbooks/onboard-repo.md"
    line: 82
    severity: low
    blocking: false
    message: |
      The "## 5. Set this serve's limits" heading now has no blank line before the following paragraph.
      Add one so the Markdown renders consistently with the other sections.
---
The change is documentation only. It rewrites the README, getting-started, onboarding runbook and two label mentions to the flow in the issue (install App, init, merge, setup, fix manual rows, dry run, live). The flow matches the issue, and the `init --pr` and `setup --check` flags it names exist in the CLI tests. Approved.

## Checks
- Lint: skipped; the change touches only Markdown and the repository has no Markdown lint step that I ran.
- Diff coverage: skipped; the change adds no code lines.
- I did not run the CLI. I could not confirm the `ard setup` row names or the exit code 2 from the code. `internal/cli/cli_test.go` still shows a "setup stub" case, so the docs depend on #345 and #346 having landed.
