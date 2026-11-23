---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change posts one notice per scanner failure streak and one on recovery. It persists the streak only after a successful post, honours dry-run, and posts only the scanner name and a sanitised reason class. It matches plan.md, and the tests cover each class, restart, post failure, dry-run, a missing status issue and a planted marker. Approved.

## Checks
- `go build ./...` succeeds.
- `go test ./internal/scanner/ ./internal/servecmd/` passes.
- Lint: not run this round. The previous round found `make lint` stopped on a parse error in a testdata file the change does not touch.
- Diff coverage: not measured. Reading the tests, every branch in `streak.go` is exercised except the log-only error paths.

## Previous findings
- internal/servecmd/scanners.go (no StatusIssue wiring test): resolved. `TestScannerPipelineStatusIssueWired` covers it.
- internal/scanner/streak.go (nil Log skips posting): resolved. `noteRun` falls back to a discard logger.
