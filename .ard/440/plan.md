# Plan for #440

## Implementation

### Steps
1. `internal/committer/transient_test.go`, struct `flaky`: add a new field `onFail func()`. In `(*flaky).Push`, just before the final `return fmt.Errorf("gitx: push: %w: ...")`, call `f.onFail()` when it is non-nil. The hook fires only on the transient-error path, so cancelling there means the commit is about to enter `core.transientWait` (`internal/committer/committer.go:140`). No other test sets the field, so their behaviour is unchanged.
2. Same file, `TestTransientCancelledInBackoff`: replace `context.WithTimeout(..., 200*time.Millisecond)` with `ctx, cancel := context.WithCancel(context.Background())` and `defer cancel()`. Build the double as `f := &flaky{Effects: e.live(), fail: 1000, onFail: cancel}`. Keep `Backoff` at one hour, so a wait that ignores cancellation hangs until the test timeout and fails loudly. Keep the assertions: `errors.Is(err, context.Canceled)` replaces `DeadlineExceeded`, since the cause is now cancellation, and `!errors.Is(err, ErrTransient)` stays. Also assert `f.pushes == 1`, which proves the cancel happened in the first backoff. Add no sleeps. The `time` import is still used by `Backoff`.
3. Verify the guard once by hand and do not commit it. Temporarily change the `<-ctx.Done()` case in `transientWait` to never fire, for example by removing it. Confirm the test then hangs or fails, then revert. Confirm `git diff` touches only `transient_test.go`.

### Test plan
- `go test -count=500 -race -run TestTransientCancelledInBackoff ./internal/committer/` passes. Run it again while the CPU is loaded, with several parallel `go test` runs.
- Run `go test -race ./internal/committer/` to confirm the other `flaky` users (`TestTransientRetry`, `TestTransientPushLanded`, `TestTransientExhausted`, the code and init lanes) are unaffected.
- Run `make lint test`.
- Failure case: the mutation in step 3 must make the test fail.
- Edge case: the context is already cancelled when `Push` returns, so check that no step between the failed push and `transientWait` uses ctx in a way that returns a different error. For example, a landed-check fetch on retry runs only after the wait. If the test shows an error other than `context.Canceled`, assert on `errors.Is(err, context.Canceled)` only and note it in the PR.

### Risk
Low. Test-only change in one file. No production code, public interface, migration, or concurrency change. No protected path from `merge.protected_paths` is touched.

### Size
S. Files touched: `internal/committer/transient_test.go` (about 10 changed lines).
