---
role: engineer
status: done
attempt: 1
---
Rewrites the flaky search-spacing test so it no longer measures the sixth search's wait from the wrong instant. It now has no upper-bound wall-clock checks and cannot fail on a slow node.

## Changes
- internal/github/transport_test.go: `TestRetryTransportSearchSpacing` split into two subtests. "burst and non-search" uses a one-hour-interval limiter: 5 searches pass, the sixth with a 1s deadline errors at once, and a non-search request still succeeds. "spacing" asserts only that the sixth search completes at least 95ms after the start. No production change.

## Testing
- `go test -count=300 -race ./internal/github -run TestRetryTransportSearchSpacing`: pass.
- `go test ./...`: pass.
- Mutation check (scratch, reverted): burst 5 changed to 6 made both subtests fail.

## Open issues
- The parallel-load run and the other mutations (interval change, skipping the Wait call) were not run.
