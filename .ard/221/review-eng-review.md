---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/scanner/streak.go"
    line: 70
    severity: low
    message: |
      noteRun returns silently when p.Log is nil, so no notice is posted at all.
      Whether a notice is posted should not depend on a logger being set. Use the Pipeline's nil-safe
      logger accessor or a discard logger. Serve wiring always sets Log, so production is unaffected.
  - file: "internal/servecmd/scanners.go"
    line: 32
    severity: low
    message: |
      plan.md named a servecmd test that StatusIssue is wired; none was written. If the field were
      dropped, nothing would fail and streak notices would silently stop. Add a small test or extract
      the pipeline construction.
---
The change matches plan.md and the issue: one notice per streak and one on recovery, a sanitised class, state written only after a successful post, and dry-run honoured. No blocking problems; the two low findings from the previous round remain.

## Checks
- Lint: not re-run this round. The previous round's `make lint` showed nothing on the changed files.
- Tests: `go test ./internal/scanner/ ./internal/servecmd/` passes.
- Diff coverage: not re-measured. Last round streak.go was at 100% except `noteRun` (87.1%), whose uncovered lines are the log-only error branches.

## Previous findings
- internal/scanner/streak.go (nil Log skips posting): still present
- internal/servecmd/scanners.go (no StatusIssue wiring test): still present
