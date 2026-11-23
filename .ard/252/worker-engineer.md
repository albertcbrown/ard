---
role: engineer
status: done
attempt: 1
---
When a done review no longer covers the PR head because code changed, the merger now removes that role's done label once per head, so the reviewer re-runs at the current head instead of the PR staying stuck. The misleading log hint is fixed.

## Changes
- `internal/state/events.go`: new `review_rerequest` event kind, the once-per-head record.
- `internal/merger/merge.go`: `rerequestReview` runs when `prgate.Covers` is false. It skips if the done label is absent or an event exists for this PR and head. It removes the label and logs `merge.review_rerequested`, then appends the event. In dry-run it logs `merge.would_rerequest` and writes only the ledger. A failed label removal records no event, so the next tick retries; a failed event read fails closed. The no-close-out-record case only logs, with a corrected hint.
- `internal/merger/loop.go`: `RemoveLabel` added to the merger's Effects subset.
- `internal/merger/doc.go`, `docs/proposal.md`: merger section and pause/dry-run paragraph describe the re-request.
- `internal/merger/loop_test.go`: new tests; an existing stale-review test was adjusted because the first tick now re-requests.

## Testing
`go test ./internal/merger/ ./internal/state/` passes, and `go test ./...` showed no failures. New tests cover once per head (a new head re-requests again), a main-only move, dry-run ledger-only, a missing done label, a label error retried, and the no-record case. I confirmed in `internal/reviewer/candidates.go` that a request label with no done, running or changes label is claimable, so removing the done label is enough. `make lint` printed only a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a deliberately broken fixture that this change did not touch.

## Open issues
- In dry-run no event is written, so each tick logs `merge.would_rerequest` again and appends to the ledger.
- The kill switch needs no code: the merger loop is skipped while paused. The existing `Claims` test covers this.
