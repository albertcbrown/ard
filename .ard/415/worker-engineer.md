---
role: engineer
status: done
attempt: 1
---
Adds the `p`, `+` and `-` keys to the serve command center: `p` toggles the local pause file, `+`/`-` change the pool cap at runtime (bounded to 1..min(2×config,16)). The override survives config reloads until `max_pods` on origin/main changes, never revokes held slots, and never writes the config file.

## Changes
- `internal/servecmd/slots.go`, `controls.go`: the in-memory slot override and the UI controls (pause, unpause with loop wake, slot up/down). `wiring.initSlots` and `wiring.reloadMax` are new.
- `internal/servecmd/serve.go`: the serve logger is now wired into the override and the Config hook calls `reloadMax`.
- `internal/servecmd/snapshot.go`, `internal/snapshot/snapshot.go`: pool view shows config max, override and draining.
- `internal/ui/`: keys, confirm-to-unpause, toasts, header, help text, golden files.
- `docs/proposal.md`: concurrency and kill-switch bullets.
- Tests for the override, controls, UI keys and snapshot.

## Testing
`go test ./internal/servecmd/...` passes. Added `TestWiringLogsOverride`, which goes through the wiring and checks that `pool.max_override` is logged for a key change and for the config-change clear.

## Review findings
- `internal/servecmd/serve.go:464` (high, blocking): fixed. `w.initSlots()` now sets the override's logger to the serve logger, so `pool.max_override` is emitted in a real run.
- `internal/servecmd/serve.go:324` (medium): partly fixed. The reload step is now the `wiring.reloadMax` method and the new test uses it against the wiring. It is not tested through a real pool or `serveLoops`.
- `internal/servecmd/controls.go:66` (low): not changed. The reviewer judged the impact low and the next reload corrects it.

## Open issues
- `go test -race ./internal/servecmd` reports a data race in `TestRunWithUI` (`uilog_test.go`, a test-local variable). I did not touch that file or fix it, and the Makefile does not use `-race`. Without `-race` the package passes.
- `TestTransientCancelledInBackoff` is a flaky committer test from #357, unrelated to this change.
