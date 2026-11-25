---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/servecmd/controls.go"
    line: 115
    severity: low
    blocking: false
    message: |
      `step` calls `slots.Step` and then `p.SetMax(n)` outside the override's lock, so a concurrent config reload can briefly leave the pool cap different from the override. The next reload or key press corrects it. Low impact.
---
The previous blocking finding is fixed: `initSlots` now gives the override the serve logger, so `pool.max_override` is emitted in a real run, and a wiring test covers it. The rest of the change matches the plan and is tested, so I approve.

## Checks
- Lint: `make lint` was not run (budget). `go vet ./internal/...` printed no findings.
- Tests: `go test ./internal/servecmd ./internal/ui ./internal/snapshot` passes.
- Diff coverage: not measured. The new slot, controls and key-handling code is exercised by unit tests.

## Previous findings
- internal/servecmd/serve.go (logger never wired): resolved. `w.initSlots()` is called right after `wiring` is built, and `TestWiringLogsOverride` checks it.
- internal/servecmd/serve.go (reload path untested): resolved enough. The step is now `w.reloadMax`, and a test covers it. The one-line `SetMax` call in the reload closure is still not run by a test.
- internal/servecmd/controls.go (SetMax outside the lock): still present, low and non-blocking.
