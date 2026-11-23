---
role: engineer
status: done
attempt: 1
---
Adds a wake channel to the controller, fed by SIGUSR1 in `ard serve`, so every loop runs one tick immediately without a restart. The tick goes through the normal path (config reload, kill switch, pool) and is logged as `controller.wake`.

## Changes
- `internal/controller/controller.go`, `scheduler.go`: new `Deps.Wake` channel; a goroutine fans each wake out to a per-loop buffered channel (pending wakes coalesce) and exits on shutdown; `runner.run` also wakes on it.
- `internal/servecmd/wake_unix.go`, `wake_other.go`, `serve.go`: `wakeSignals` forwards SIGUSR1 on Unix (nil elsewhere) and is wired into `Deps`.
- Tests: four scheduler wake tests (tick now, kill switch, coalescing without overlap, wake after stop) and a SIGUSR1 delivery test.
- Docs: proposal kill switch paragraph, Gate 2 runbook (`kill -USR1`), removed #161 from TODO.

## Testing
`go vet` and `gofmt -l` clean on the changed packages; `go test ./internal/controller ./internal/servecmd` pass. Did not run the full `make test` or `make lint`.

## Open issues
- Unix only, as the plan states.
