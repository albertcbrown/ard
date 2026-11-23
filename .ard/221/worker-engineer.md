---
role: engineer
status: done
attempt: 1
---
Scanner failure streaks are now announced on the status issue: one comment when a streak starts (guard rule name, `scan_failed` or `max_open`) and one on recovery. The streak is kept in the scanner's `meta` table, so a restart does not repost, and dry-run logs instead of posting. Comments carry the scanner name and reason class only.

## Changes
- `internal/scanner/streak.go`: class mapping, sanitising, comment bodies and `noteRun`.
- `internal/scanner/store.go`, `pipeline.go`: `Store` gains `Meta`/`SetMeta`, and `Pipeline` gains `StatusIssue`, with `noteRun` called from `record`.
- `internal/servecmd/scanners.go`: pipeline construction extracted to `scannerPipeline`, wiring `StatusIssue`.
- Tests: streak and pipeline streak tests, harness fakes, and a servecmd wiring test.
- `docs/proposal.md` (and `docs/TODO.md` if listed): describe the notice.

## Testing
Ran `go test ./internal/scanner/ ./internal/servecmd/`, `go vet` and gofmt; all pass.

## Review findings
- `internal/servecmd/scanners.go:32`: fixed. Extracted `scannerPipeline` and added `TestScannerPipelineStatusIssueWired` in `scanners_test.go`, which checks `StatusIssue` is set and tracks `w.statusIssue`.
- `internal/scanner/streak.go:70`: fixed. A nil `Log` now falls back to a discard logger, so the notice is still posted.

## Open issues
None.
