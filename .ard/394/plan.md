# Plan for #394

## Implementation

### Steps

1. `internal/pool/pool.go`: add field `freed chan struct{}` to `Pool`, created in `New` with `make(chan struct{}, 1)`. Add `func (p *Pool) Freed() <-chan struct{}` (new) and unexported `func (p *Pool) signalLocked()` (new) doing a non-blocking send (`select { case p.freed <- struct{}{}: default: }`). In `SetMax`, call `signalLocked()` only when the new max is greater than the old one. Reason: releases and capacity raises coalesce into one pending signal.
2. `internal/pool/slot.go`: in `Slot.Release`, after the `delete(p.held, ...)` (inside the `released` first-call path), call `p.signalLocked()` only when the entry was actually deleted (`p.held[key] == s`); a stale handle whose key was re-granted must not signal. Keep the existing `pool.release` debug log and add the `pool.freed` debug log with `"key", s.Key()` at that point.
3. `internal/pool/demand.go`: add `func (p *Pool) DemandLoops(now time.Time) []string` (new). Under `p.mu`, return the sorted names of loops in `p.demand` where `now.Before(d.expires)` and at least one candidate has `p.held[key] == nil`. Do not mutate state (no purge), so it is a pure read. Reason: only loops that can use the slot are woken.
4. `internal/controller/controller.go`: add to `Deps`: `Freed func() <-chan struct{}` (resolved after startup hooks, because `internal/servecmd/serve.go` builds the pool in the `pool-adopt` hook, after `controller.New`), `DemandLoops func(now time.Time) []string`, and `FreedDebounce time.Duration` (0 means `DefaultFreedDebounce = 2 * time.Second`, new const). In `Run`, after the runners exist and only if `Freed != nil`, start a goroutine (tracked by `wg`, stops on `stopping`) that: waits on the freed channel; starts a `time.Timer` of the debounce; further freed signals inside the window are drained; on fire, checks `stopping`, calls `DemandLoops(time.Now())`, sends non-blocking to `r.wake` of each runner whose name is in the result (`r.wake` is buffered 1, so it coalesces with a pending wake and with SIGUSR1, and a loop mid-tick runs exactly one more tick), and logs `controller.wake` at info with `reason=slot_freed` and `loops=[...]` (skip the log and wake when the list is empty). Existing SIGUSR1 path in `Run` stays as is, and should get `reason=signal` on its `controller.wake` line only if no existing test asserts the exact line (check `TestWakeRunsTickNow`, which only counts occurrences).
5. `internal/servecmd/serve.go` (~line 270, the `controller.Deps` literal): set `Freed: func() <-chan struct{} { return w.pool.Load().Freed() }` and `DemandLoops: func(t time.Time) []string { return w.pool.Load().DemandLoops(t) }`. The controller calls `Freed` once after the hooks, when `w.pool` is stored. Guard nil: return nil from `Freed` if the pool is nil (a nil channel never fires, so the goroutine just idles). Only loops that call `Pool.Offer` appear in demand (reviewers, workers, triage), so the merger, prwatch, reaper and scanners are never woken; scanners use `Offer("scanners", d.OfferTTL, ...)` in `internal/scanner/engine_exec.go` but keep no demand when denied, so verify `DemandLoops` never returns `scanners` by a pool test.
6. Release timing check (no code change expected): `internal/worker/run.go:88`, `internal/triage/run.go:25`, `internal/reviewer/run.go:63` use `defer slot.Release()` at the top of `run`, so the release happens after `FinishWith`/close-out has written labels. The earlier `Release` calls in `loop.go` of each (`stopped` and `Inflight.Go` failure) happen before any claim. The Reaper's `releaseAdopted` (`internal/reaper/reaper.go:169`) runs after the Reaper's close-out. If reading shows any path releases earlier, keep the skip logic that already exists (held key, `:running` label, job record) and cover it with the test below.
7. `docs/proposal.md` line ~303 ("One pool of job slots"): replace "so a freed slot can sit idle until that loop's next poll" with a sentence saying a released slot wakes the loops with live demand within a short debounce (default 2s), which then offer as usual so class order and reservations still decide; paused controllers ignore the wake; the merger, prwatch and scanners are not woken. Also mention the wake in the controller section if SIGUSR1 is described there (grep `SIGUSR1` in docs/).

### Test plan

Command: `make lint test` (runs `go vet ./...`, `gofmt -l .`, `go test ./...`).

Pool, `internal/pool/pool_test.go` (uses the existing `newPool` and `clock` helpers):
- `TestFreedSignalOncePerRelease`: grant a slot, release it, `<-p.Freed()` receives one value; a second `Release` on the same handle sends nothing (select with default).
- `TestFreedCoalesces`: grant 3, release all 3, exactly one value is buffered.
- `TestFreedStaleHandleNoSignal`: release, re-grant same key, release the stale handle again, no signal.
- `TestSetMaxRaiseSignals`: raising signals; lowering or equal does not.
- `TestDemandLoops`: loops with live demand and an unheld candidate are returned, sorted; an expired entry (advance the clock past ttl) is excluded; a loop whose only candidate is now held is excluded; a loop with no demand and the `scanners` loop after a denied offer are absent.

Controller, `internal/controller/scheduler_test.go` (use `synctest.Test`, as the existing tests do, no real sleeps beyond synctest's fake time):
- `TestFreedWakesDemandLoopOnly`: loops `a` (demand) and `b` (none); send on the freed channel; after the debounce `a` has one extra tick, `b` none; log has `reason=slot_freed`.
- `TestFreedDebounceCoalesces`: three freed signals inside the window produce one extra tick for `a` and one `controller.wake` line.
- `TestFreedNoDemandNoWake`: `DemandLoops` returns nil, no tick and no log.
- `TestFreedWhilePausedRunsNoTick`: pause func returns paused; a freed wake leaves `ticks() == 0`; a SIGUSR1 `Wake` send still triggers the loop to check pause (existing `TestWakeRespectsKillSwitch` semantics) and, when not paused, runs every loop.
- `TestFreedCoalescesWithScheduledTick`: a wake delivered while the loop is mid-tick (blocking `tick` func) yields exactly one more tick after it returns, not two.
- `TestFreedDuringShutdown`: signal racing `stopping` starts no tick.

Re-claim guard, in the worker loop's test file (`internal/worker/`, mirror an existing loop test): a finished job's issue has the `:running` label or a job record, or its key is still held, and a woken tick does not offer or claim it. Do the same for the held-key case via the pool (`DeniedDuplicate`).

### Risk

Medium. Touches the controller's scheduling goroutines (concurrency: debounce goroutine joins `wg` and must stop on `stopping`) and the pool's lock-protected release path. No migrations or security-sensitive code. No protected paths from `merge.protected_paths` are touched (`docs/proposal.md` and `internal/` are not listed). Public interfaces gain `Pool.Freed`, `Pool.DemandLoops` and three optional `controller.Deps` fields; existing callers are unaffected when they are unset.

### Size

M (roughly 150 lines of code and 250 of tests). Files: `internal/pool/pool.go`, `internal/pool/slot.go`, `internal/pool/demand.go`, `internal/pool/pool_test.go`, `internal/controller/controller.go`, `internal/controller/scheduler_test.go`, `internal/servecmd/serve.go`, `docs/proposal.md`, and one worker loop test file.
