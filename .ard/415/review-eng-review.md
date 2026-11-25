---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/servecmd/serve.go"
    line: 464
    severity: high
    blocking: true
    message: |
      The `pool.max_override` log is never emitted in production: `slotOverride.log` is never set.
      `w.slots` is a zero-value `slotOverride`, and `logf` returns early when `log` is nil. The issue requires logging `pool.max_override` (key changes and the clear on a config change). Only the unit tests, which inject a logger, see it. Set `w.slots.log` to the serve logger where `wiring` is built, and add a test that goes through the wiring.
  - file: "internal/servecmd/serve.go"
    line: 324
    severity: medium
    blocking: false
    message: |
      The reload path (`SetMax(w.slots.Effective(...))`) has no test that goes through serveLoops or the reload closure. The acceptance criteria ask for tests with config reloads. The tests only call `slotOverride.Effective` directly, so a regression in this wiring would pass. Consider extracting the reload step into a function and testing it against a real pool.
  - file: "internal/servecmd/controls.go"
    line: 66
    severity: low
    blocking: false
    message: |
      `step` calls `slots.Step` and then `p.SetMax(n)` outside the override's lock, so a concurrent config reload can interleave and leave the pool cap briefly different from the override. Low impact, since the next reload corrects it.
---
The slot override and pause key logic is sound and tested, but the required `pool.max_override` log is never emitted in a real run because the logger is never wired in, so this needs changes.

## Checks
- Lint: no repository lint entry point was run (budget); `go build ./...` and `go vet ./internal/...` showed no errors.
- Tests: `go test ./internal/servecmd ./internal/ui ./internal/snapshot` passes.
- Diff coverage: not measured; the new controls and slot functions are exercised by unit tests, but the serve.go reload wiring is not.
