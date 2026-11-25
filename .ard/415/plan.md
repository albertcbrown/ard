# Plan for #415

## Implementation

### Steps
1. `internal/pool/pool.go` (`SetMax`, ~l.129): no behaviour change; add a read of the cap already in `Stats().Max`. Reason: the override logic lives in servecmd, the pool only needs to keep never revoking held slots (already true). Add a test only (see Test plan).
2. New `internal/servecmd/slots.go`: type `slotOverride` (new) with a mutex, `now func() time.Time`, `n int` (0 = none) and `base int` (config `max_pods` when the override was set). Methods:
   - `Set(n, cfgMax int, limit...)`: clamps to `1..min(2*cfgMax,16)`, stores `base=cfgMax`, logs `pool.max_override` (`from`, `to`, `config`, `reason=key`).
   - `Step(delta, cfgMax int) (int, bool)`: current effective value ± delta, clamped; returns the new value and whether it changed (for the toast; "at limit" otherwise).
   - `Effective(cfgMax int) int`: if no override, `cfgMax`; if `cfgMax != base`, clear and log `pool.max_override` with `reason=config_changed`, return `cfgMax`; else the override.
   - `Clear()`.
3. `internal/servecmd/serve.go`, Config hook (~l.262): replace `p.SetMax(c.MaxPods)` with `p.SetMax(w.slots.Effective(c.MaxPods))`. Add field `slots slotOverride` to `wiring`. Reason: this is the reload fix; the override survives reloads until `max_pods` on origin/main differs from the value it was set against. `.ard/config.yaml` is never written. `adoptPool` at startup keeps `w.MaxPods`.
4. New `internal/servecmd/controls.go`: type `controls` (new) implementing the UI's `ui.Controls` interface (step 6), built in `serveLoops` after `w.snap` is set and passed to `runWithUI`:
   - `Pause() error`: create `pausePath` with `os.OpenFile(O_CREATE|O_WRONLY|O_EXCL, 0o600)`; an existing path is success. Make the parent dir (`MkdirAll`, 0o700).
   - `Unpause() error`: `os.Lstat`; refuse a directory (error, so the operator removes it by hand); otherwise `os.Remove`, then `w.wakeLoops("unpause", loopNames(selected)...)` (check `Controller.WakeLoops` for how it treats names; use the selected loop names so that no empty-name case arises). Reason: the loops may be sleeping up to one interval.
   - `SlotUp()/SlotDown() (msg string, err error)`: use `w.slots.Step`, then `p.SetMax(...)` on `w.pool.Load()` (nil pool before startup: toast "pool not ready"), wake loops with `slot_freed`-style reason only when raised (`SetMax` already signals `Freed`).
   - Config max comes from `w.cfg.Load().MaxPods` (fall back to `config.DefaultMaxPods` when nil).
5. `internal/snapshot/snapshot.go`: extend `PoolView` with `ConfigMax int` and `Override bool` (new), filled from `Sources.Config` and a new optional `Sources.Override func() (int, bool)`; wire it in `internal/servecmd/snapshot.go`. `Used > Max` is "draining". Also expose in `PauseView` nothing new; Reason already distinguishes `pause-file` from `label`/`config`/`local-config`.
6. `internal/ui/keys.go`, `model.go`, `view.go`: add `type Controls interface { Pause() error; Unpause() error; SlotUp() (string, error); SlotDown() (string, error) }` (new) and a `Model.ctl Controls` field (change `New` to take it; nil keeps the old read-only behaviour). Keys in `handleKey`:
   - `p`: if the snapshot shows a `pause-file` reason, set `confirmUnpause` and show "unpause? y/enter"; on `y`/`enter` call `Unpause()`, else cancel (mirror `confirmQuit`). Otherwise call `Pause()` at once. If the only pause reasons are `label`, `config`, `local-config`, `unreadable`, do not touch anything: toast "paused by <reason>: remove the ard:pause label on the status issue / set paused: false on main" (text per reason).
   - `+` / `-`: call `SlotUp/SlotDown`, store the toast with an expiry (use a `toast string` and `toastUntil time.Time`; clear on `tickMsg`; use an injectable clock `Model.now`).
   - Header (`view.go` l.56): `max_pods 4 (config 3)` when `Override`, `max_pods 3` otherwise, plus "draining" when `Used > Max`; show `PAUSED (file)` badge from `s.Pause`. Help text (`?`): list `p + -` and the line "dry-run: the pause file and slot override still apply locally; they are local controls, not GitHub effects". Strip all text with `Clean`.
7. `docs/proposal.md`: Concurrency bullet (l.543): add the runtime override (keys, bounds `1..min(2×config,16)`, kept until `max_pods` on origin/main changes, never kills jobs, config never written). Kill-switch bullet (l.544): mention `p` in the UI toggles the local pause file only and does not lift the label or config sources. Run the repo's docs check (`internal/docscheck`, part of `make test`).

### Test plan
Command: `make lint test` (also `go test ./internal/servecmd/... ./internal/ui/... ./internal/snapshot/... ./internal/pool/...` while developing).
- `internal/servecmd/slots_test.go` (new), fake clock and fake log:
  - `TestOverrideClampsToBounds`: config 3 → max 6; config 10 → max 16; min 1; `Step` at a bound reports no change.
  - `TestOverrideSurvivesReload`: set 5 against config 3; `Effective(3)` repeatedly returns 5.
  - `TestOverrideClearedWhenConfigChanges`: `Effective(4)` returns 4, logs `pool.max_override` reason `config_changed`; a later `Effective(4)` returns 4.
  - `TestOverrideNeverRevokes` (pool): hold 3 slots, set max 1, `Stats` shows Held 3, Max 1, no slot released; a new Offer is denied until held < 1.
  - `TestConfigFileUntouched`: run set/reload with a temp repo and assert `.ard/config.yaml` bytes and mtime unchanged.
- `internal/servecmd/controls_test.go` (new): `Pause` creates the file, is idempotent; `Unpause` removes it and sends one wake (fake controller or `ctl` stub); `Unpause` on a directory errors and leaves it; works in dry-run (`e.DryRun`).
- `internal/ui/ui_test.go` (+ golden files in `internal/ui/testdata`): `p` with no pause calls Pause; `p` with a `pause-file` snapshot asks to confirm, `n` cancels, `y` unpauses; `p` with only a `label` or `config` reason calls nothing and shows the lift hint; `+`/`-` toast and header `max_pods 4 (config 3)`; "draining" shown when Used > Max; nil Controls keeps keys inert; help text mentions dry-run.
- `internal/snapshot/snapshot_test.go`: `PoolView.ConfigMax` and `Override` filled.
- Failure cases: pause-file write error shows an error toast and does not crash; `+` before the pool exists toasts "pool not ready".

### Risk
Medium. The pool cap and kill-switch handling are the core of the controller; a wrong override would change concurrency, and a wrong `p` could hide a pause. Mitigations: the override only calls `SetMax`, which never revokes slots; `p` only touches the local pause file. `.ard/config.yaml` is a protected path but is not changed. No migrations or public CLI flags change; `ui.New` signature changes (internal only). Concurrency: `slotOverride` needs its own mutex because the UI goroutine and the controller's Config hook both call it; run tests with `-race`.

### Size
M to L (roughly 350 to 600 changed lines including tests and golden files). Files: `internal/servecmd/slots.go` (new), `internal/servecmd/controls.go` (new), `internal/servecmd/serve.go`, `internal/servecmd/ui.go`, `internal/servecmd/snapshot.go`, `internal/snapshot/snapshot.go`, `internal/ui/keys.go`, `internal/ui/model.go`, `internal/ui/view.go`, tests and `internal/ui/testdata`, `docs/proposal.md`.
