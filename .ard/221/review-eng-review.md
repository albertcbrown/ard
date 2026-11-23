---
role: eng-review
status: done
attempt: 2
verdict: approve
---
The change posts one notice per scanner failure streak and one on recovery. It persists the streak only after a successful post, honours dry-run, and posts only the scanner name and a sanitised reason class. The two earlier findings are fixed, and the tests cover each outcome class, a restart, a post failure, dry-run and a planted marker. Approved.

## Checks
- `go build ./...` succeeds.
- `go test ./internal/scanner/ ./internal/servecmd/` passes.
- `make lint`: `go vet` stops on a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file the change did not touch, so I saw nothing on the changed files. I did not run the rest of the lint entry point.
- Diff coverage: not measured. The tests exercise every added branch in `streak.go` except the log-only error paths.

## Previous findings
- internal/servecmd/scanners.go (no StatusIssue wiring test): resolved. `TestScannerPipelineStatusIssueWired` covers it.
- internal/scanner/streak.go (nil Log skips posting): resolved. `noteRun` falls back to a discard logger.
