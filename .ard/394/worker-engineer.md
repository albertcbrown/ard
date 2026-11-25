---
role: engineer
status: done
attempt: 1
---
A released job slot (or a raised `max_pods`) now wakes the loops that have live demand after a short debounce, so a freed slot is refilled within seconds instead of waiting for the next poll.

## Changes
- `internal/pool`: `Pool.Freed()` (buffered 1, non-blocking send) is signalled on the first effective `Slot.Release` and when `SetMax` raises the cap; `pool.freed` debug log. `Pool.DemandLoops(now)` returns the sorted loops with live demand and an unheld, non-scanner candidate (pure read).
- `internal/controller`: new `Deps` fields `Freed`, `DemandLoops`, `FreedDebounce` (default 2s). A goroutine debounces signals and sends one non-blocking wake to each demand loop's runner, logging `controller.wake` with `reason=slot_freed`. Wakes coalesce with pending ticks and SIGUSR1, and the pause gate still applies.
- `internal/servecmd/serve.go`: passes the pool lazily, since it is built in a startup hook.
- `docs/proposal.md`: pool paragraph and kill-switch paragraph describe the wake.
- Tests: pool (`Freed` once, coalescing, stale handle, `SetMax`, `DemandLoops`) and a new `internal/controller/freed_test.go` (per-loop wake, debounce, no demand, paused, mid-tick coalescing, shutdown), all with synctest.

## Testing
`make lint test` passes. Release timing: the job runners release by `defer` after close-out, so none releases early.

## Open issues
- The issue says scanners keep no demand, but the scanner engine offers with a 10m TTL and does keep demand. `DemandLoops` therefore skips scanner-class candidates explicitly.
- I did not add a separate worker-loop re-claim test. Existing held-key (`DeniedDuplicate`), `:running` label and job-record skips are unchanged, and no early release path exists.
