---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/effects/approve.go"
    line: 22
    severity: low
    message: |
      Not-logged-in detection relies on matching gh's stderr text ("authentication", "gh auth login"), which can change between gh versions. A `gh auth status` pre-check, as plan.md suggested, would be sturdier. Falling back to the URL on any failure is not needed; the current behaviour is acceptable.
---
The change follows plan.md: the new effect method is not on the App client, gh runs with fixed arguments and no shell, dry-run records the effect, and the UI confirm flow defaults to No. The affected packages' tests pass. Approved.

## Checks
- Lint: `make lint` printed only dependency downloads and a parse error in a roles testdata file, which the change did not touch. I could not confirm a clean result from the truncated output.
- Tests: `go test` on effects, servecmd, ui, triage, snapshot and docscheck all pass.
- Diff coverage: not run. The diff adds tests for effects, the UI (golden file), servecmd, triage and snapshot.
