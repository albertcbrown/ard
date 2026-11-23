---
role: engineer
status: done
attempt: 1
---
Each scanner now posts one comment on the pinned status issue when a failure streak starts and one when it recovers, so a scanner that stops working no longer stops silently. The streak is kept in the scanner's existing `meta` table, so a restart does not repost.

## Changes
- `internal/scanner/streak.go` (new): outcome classification (guard rule name, `scan_failed`, `max_open`; `timeout` counts as `scan_failed`; global-cap, error, no-slot, paused and skipped are neutral), class sanitising to `[A-Za-z0-9._:-]` and at most 64 characters, fixed notice and recovery text, and `Pipeline.noteRun`.
- `internal/scanner/pipeline.go`: `StatusIssue func() int` field; `record` calls `noteRun` after `RecordRun`. Errors are logged and never change the run result.
- `internal/scanner/store.go`: `Store` gains `Meta` and `SetMeta` (`*scannerdb.DB` already has them).
- `internal/servecmd/scanners.go`: wires `StatusIssue` to the status issue number.
- `docs/proposal.md`: Observability bullet describes the notice.

## Testing
`go test ./...` passes, and `go vet ./...` is clean. New tests are in `streak_test.go` and `pipeline_streak_test.go`:
- classification table;
- sanitising;
- one post per streak and one recovery for scan_failed, timeout, max_open and guard;
- restart mid-streak;
- neutral outcomes;
- planted marker absent from posts;
- dry-run logs and writes no state;
- no status issue, then it posts once the issue is known;
- post failure retries on the next run;
- old state without the key.

## Open issues
- The planned wiring test in `internal/servecmd/scanners_test.go` was not added: `scannerLoop` needs the full executor and pool wiring, so it is a one-line assignment checked by the build only.
- `gofmt -l` flags `internal/roles/testdata/docs/skips/src/_x.go`, an intentionally broken fixture. It is not part of this change.
- `docs/TODO.md` lists #221 as an open item with no checkbox, so it is unchanged.
