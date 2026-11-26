---
role: engineer
status: done
attempt: 1
---
Makes TestTransientCancelledInBackoff cancel from inside the `flaky` test double on the first transient push failure instead of by a 200 ms wall-clock timeout, so cancellation always lands in the backoff wait and the test no longer flakes under load.

## Changes
- internal/committer/transient_test.go: `flaky` gets an optional `onFail` hook, called just before the transient error is returned. The test uses `context.WithCancel` with `onFail: cancel`, keeps the one-hour backoff, expects `context.Canceled` (not `ErrTransient`) and asserts exactly one push. No sleeps added.

## Testing
- `go test -count=100 -race -run TestTransientCancelledInBackoff ./internal/committer/`: pass.
- `go test -race ./internal/committer/`: pass.
- Mutation check (reverted): making the backoff wait's context case never fire made the test hang until the test timeout.
- gofmt clean on the changed file.

## Open issues
- I did not run `make lint test`, the 500-iteration run, or runs under CPU load.
