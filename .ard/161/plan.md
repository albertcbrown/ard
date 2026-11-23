# Plan for #161

## Implementation

### Steps
1. `internal/controller/controller.go`: add `Wake <-chan struct{}` to `Deps` (nil means no wake; doc: each receive makes every loop run one tick now). Add `wake chan struct{}` (buffer 1, new) to `runner`, created in `Run` where runners are built. In `Run`, after `controller.start`, start a goroutine: on each `Deps.Wake` receive, log `controller.wake` (Info, with `loops` names), then non-blocking send to every runner's `wake` channel (a pending wake coalesces). The goroutine exits when `stopping` closes; add it to the `wg` or make it exit cleanly so shutdown still drains.
2. `internal/controller/scheduler.go`: in `runner.run`, add `case <-r.wake:` to the first select beside `t.C` (stop the timer). The existing `stopping` re-check follows, then `r.tick` as today. A wake therefore goes through `tick` unchanged: config reload, `Pause.Check` for claiming loops (kill switch), and the job pool/inflight. Ticks never overlap, because wake is only read between ticks; a wake that arrives during a tick runs one more tick right after it. Next wait is computed from `nextWait` as usual. Log `loop.woken` at Debug in `run` only if useful.
3. New `internal/servecmd/wake_unix.go` (`//go:build unix`): `func wakeSignals(ctx context.Context) <-chan struct{}` using `signal.Notify(ch, syscall.SIGUSR1)`, forwarding to an unbuffered-or-1-buffer channel and calling `signal.Stop` when ctx is done. New `wake_other.go` (`//go:build !unix`) returns nil.
4. `internal/servecmd/serve.go` (~line 231): set `Wake: wakeSignals(ctx)` in `controller.Deps`. Do not touch `forceOnSecondSignal`.
5. Docs: `docs/proposal.md` (Kill switch / scheduler section near the line starting "**Kill switch.**", and the `ard serve` operations text): add that `SIGUSR1` makes every loop run one tick now, behind the kill switch and pool, logged as `controller.wake`, Unix only, coalesced. `docs/runbooks/gate-2.md` "Moving faster than the poll": replace the restart advice with `kill -USR1 <pid of ard serve>`, and drop "Until #161 adds". Update `docs/TODO.md` if it lists #161.

### Test plan
Command: `make test` (`go test ./...`), plus `make lint`.
- `internal/controller/scheduler_test.go` `TestWakeRunsTickNow`: loop with long interval, send on `Deps.Wake` after the first tick; expect a second tick well before the interval and one `controller.wake` log line.
- `TestWakeRespectsKillSwitch`: claiming loop with a Pauser returning paused; wake; expect no `Tick` call and a `loop.paused` log.
- `TestWakeDuringTickCoalesces`: slow tick, send three wakes; expect no overlap and exactly one extra tick.
- `TestWakeAfterStopIgnored`: wake during shutdown starts no tick and does not hang `Run`.
- Nil `Wake`: existing tests pass unchanged.
- `internal/servecmd/serve_test.go` (unix): `wakeSignals` delivers on `syscall.Kill(os.Getpid(), SIGUSR1)` and stops after ctx cancel.

### Risk
Low. Scheduler timing code is shared by every loop, so keep the change small. No protected paths, config, migrations or security-sensitive code. Concurrency: wake sends are non-blocking, and the only new goroutine ends on shutdown. A new operator-visible signal is a small public interface; any process with the same uid can send it, but it only brings forward ticks that are already allowed.

### Size
M. Files: internal/controller/controller.go, internal/controller/scheduler.go, internal/controller/scheduler_test.go, internal/servecmd/serve.go, internal/servecmd/wake_unix.go (new), internal/servecmd/wake_other.go (new), internal/servecmd/serve_test.go, docs/proposal.md, docs/runbooks/gate-2.md, docs/TODO.md (if needed).
