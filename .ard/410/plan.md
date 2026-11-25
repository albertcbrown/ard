# Plan for #410

## Implementation

### Steps
1. Edit only `internal/github/transport_test.go`, function `TestRetryTransportSearchSpacing` (around line 235). No production change: `retryTransport.RoundTrip` (`internal/github/transport.go`) calls `t.search.Wait(ctx)` on the `search *rate.Limiter` field, and the test already replaces that field, so no clock seam is needed. Reason: the lower bound from `start` and the context deadline below need no fake clock.
2. Split the test into deterministic checks, all using `r := newRig(t, time.Minute, status(200))`:
   - Burst and non-search check: set `r.rt.search = rate.NewLimiter(rate.Every(time.Hour), 5)`. Send 5 `GET /search/issues` requests with `context.Background()`; each must succeed. Then send a sixth with `context.WithTimeout(ctx, time.Second)`. `Limiter.Wait` returns an error immediately when the wait would pass the deadline, so require `err != nil` (and no HTTP request made). This fails if burst is not 5 or the limiter is removed, and uses no wall-clock measurement. Then send `GET /repos/o/r` with the same short deadline context; it must succeed, which shows non-search requests are not limited. Drop the `> 80ms` and `> 50ms` upper bounds.
   - Spacing check (new subtest or block): set `r.rt.search = rate.NewLimiter(rate.Every(100*time.Millisecond), 5)`. Record `start := time.Now()` before the burst, send 5 searches, then a sixth with `context.Background()`. Assert `time.Since(start) >= 100*time.Millisecond - 5*time.Millisecond` (small tolerance for the limiter's first-reservation instant). Time only moves forward, so a slow burst can only make the elapsed time larger. Drop the "since before" measurement. No upper bound.
3. Keep the helper `resp.Body.Close()` calls. Check the error return of the non-search request instead of ignoring it, so `resp` is never nil.

### Test plan
- Command: `go test -count=500 -race ./internal/github -run TestRetryTransportSearchSpacing`. Also run it under load, for example 4 parallel copies of that command, and run `go test ./...`.
- Case burst-limit (`transport_test.go`): 5 searches pass, the sixth with a 1s deadline errors immediately.
- Case spacing: the sixth search completes at least about 95ms after `start`.
- Case non-search: `/repos/o/r` succeeds with the search limiter exhausted.
- Mutation check in a scratch run only, reverted afterwards: change burst 5 to 4 or 6, change the interval to 10ms, or make the transport skip `t.search.Wait`. Each must fail at least one check.

### Risk
Low. Test-only change in one file, no public interface, protected path, migration or security-sensitive code touched. The remaining wall-clock dependence is the lower bound on elapsed time, which cannot fail on a slow node.

### Size
S. Files touched: `internal/github/transport_test.go`.
