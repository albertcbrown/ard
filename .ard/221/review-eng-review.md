---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change follows the issue and plan. It posts one notice per failure streak and one on recovery, and it keeps the streak state only after a successful post. Both previous findings are resolved, and the failure paths are tested. Approved.

## Checks
- Lint: not re-run this round. The previous round's `make lint` output showed nothing on the changed files.
- Tests: `go test ./internal/scanner/ ./internal/servecmd/` passes.
- Diff coverage: not measured. The tests exercise each added branch in `streak.go` except the log-only error paths.

## Previous findings
- internal/servecmd/scanners.go (no StatusIssue wiring test): resolved. `scannerPipeline` is extracted and `TestScannerPipelineStatusIssueWired` checks it.
- internal/scanner/streak.go (nil Log skips posting): resolved. `noteRun` falls back to a discard logger.
