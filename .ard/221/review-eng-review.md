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
      The posting logic depends on whether a logger is set, which is surprising. Pipeline
      has a logger accessor (pipeline.go:128) that handles nil; use it, or fall back to a discard logger.
      The serve wiring always sets Log, so this does not affect production.
  - file: "internal/servecmd/scanners.go"
    line: 32
    severity: low
    message: |
      plan.md named a servecmd test that StatusIssue is wired; it was not written, and the worker
      explained why (scannerLoop needs full executor wiring). If StatusIssue were dropped,
      nothing would fail and streak notices would silently stop. Consider a small test
      or extracting the pipeline construction.
---
The change matches plan.md and the issue: one notice per failure streak and one on recovery, the class is sanitised, state is written only after a successful post, and dry-run is honoured. It has no blocking problems. Two low-severity gaps remain.

## Checks
- Lint: `make lint` (go vet, gofmt) reports only `internal/roles/testdata/docs/skips/src/_x.go`, an intentionally broken fixture the change did not touch. Nothing on the changed files.
- Tests: `go test -cover ./internal/scanner/ ./internal/servecmd/` passes.
- Diff coverage: streak.go is at 100% except `noteRun` (87.1%). The uncovered lines are the meta read-error and SetMeta-error log branches.
