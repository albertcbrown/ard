---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/servecmd/scanners.go"
    line: 32
    severity: high
    blocking: true
    message: |
      The servecmd wiring test that plan.md's test plan names was never written.
      Nothing fails if `StatusIssue` is dropped from the Pipeline literal, and streak notices would then silently stop. Add a test in `internal/servecmd/scanners_test.go`, or extract the pipeline construction so a test can assert `StatusIssue` is set and returns `w.statusIssue`. The previous round rated this low, but a test plan case from plan.md that was not written is high.
  - file: "internal/scanner/streak.go"
    line: 70
    severity: low
    message: |
      noteRun returns silently when p.Log is nil, so no notice is posted at all.
      Whether a notice is posted should not depend on a logger being set. Fall back to a discard logger instead. Serve wiring always sets Log, so production is unaffected.
---
The diff is unchanged from the previous round, and the servecmd wiring test that plan.md named is still missing. Under the severity rules a missing planned test is high, so this needs changes. Everything else matches plan.md.

## Checks
- Lint and tests: not re-run. The diff is unchanged since the last round, which was clean (`make lint`, `go test ./internal/scanner/ ./internal/servecmd/`).
- Diff coverage: not re-measured. Last round's only gap was the log-only error branches in `noteRun`.

## Previous findings
- internal/scanner/streak.go (nil Log skips posting): still present
- internal/servecmd/scanners.go (no StatusIssue wiring test): still present, now rated high because plan.md named the test
