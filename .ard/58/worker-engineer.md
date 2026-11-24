---
role: engineer
status: done
attempt: 1
---
Adds a `digest` loop that posts one comment a day on the status issue with queue depth by label, jobs run, failures and spend for the previous complete UTC day. A `digest` event written after the post is the once-per-day marker, so a restart never reposts.

## Changes
- `internal/digest/` (new): loop (10-minute interval, does not claim, runs while paused) and a renderer that emits only numbers, the day and names that pass a safe-name check.
- `internal/state`: new `EventDigest`, `CountEndedSince` (jobs per stage/role, done vs failed, half-open window) and `SpendBetween`; `SpendSince` now wraps the same query.
- `internal/servecmd`: `digestLoop` wiring, registered after the budget loop and added to `knownLoops`.
- `docs/proposal.md`: Observability bullet describes the digest.

## Testing
Ran `go test` for `./internal/digest`, `./internal/state` and `./internal/servecmd`; all pass, and `go vet` is clean on them. Added digest tests (post once, restart marker, no status issue, post failure, dry run, reader error, window, queue depth, render, identity) and state tests (`TestCountEndedSince`, `TestSpendBetween`). Updated the loop-count and known-loops tests.

## Open issues
- The full `./...` suite and `make lint` were not run, only the packages above.
- Queue depth counts every `ard:` label on open non-PR issues from one list call, rather than a fixed label list.
- The plan's test names `TestRenderGolden` and `TestRestartKeepsMarker` are covered by inline-assertion tests, with no golden file.
- A failed post is logged and retried on the next tick (not returned as a tick error).
