---
role: eng-review
status: done
attempt: 2
verdict: approve
findings:
  - file: "internal/snapshot/snapshot.go"
    line: 478
    severity: medium
    blocking: false
    message: |
      `addOverride` (ConfigMax and Override on PoolView) has no test, although plan.md's test plan lists a snapshot test for it.
      Add a case in internal/snapshot/snapshot_test.go with a fake Config and Override source.
  - file: "internal/servecmd/controls.go"
    line: 92
    severity: medium
    blocking: false
    message: |
      Unpause's wake of the loops is never tested: the tests build `controls` with no loops, so `wakeLoops` is not reached. plan.md asks for a test that Unpause sends one wake.
      Add a test with a non-empty `loops` slice and a recording wake.
  - file: "internal/servecmd/controls.go"
    line: 125
    severity: low
    blocking: false
    message: |
      `step` calls `slots.Step` and then `p.SetMax(n)` outside the override's lock, so a concurrent config reload can briefly leave the pool cap out of step with the override. The next reload or key press corrects it.
---
Approved: the change follows plan.md and the pause and slot logic is tested. Two plan test cases (the snapshot fields and the Unpause wake) are missing; they are medium and do not block.

## Checks
- Lint: `make lint` was not run. `go vet` on servecmd, ui and snapshot is clean.
- Tests: `go test -race ./internal/ui ./internal/snapshot ./internal/pool` pass. `./internal/servecmd` fails under -race with a data race in `TestRunWithUI` (uilog_test.go:89-119, the `stopped` variable and the fake program). The test existed before this change and I did not check it without the change; the change only added a `nil` argument to its calls.
- Diff coverage: not measured. Slot, controls and key code is exercised except for the gaps above.

## Previous findings
- internal/servecmd/controls.go (SetMax outside the lock): still present, low and non-blocking.
- internal/servecmd/serve.go (logger wiring): resolved.
- internal/servecmd/serve.go (reload path): resolved enough; the one-line `SetMax` call in the reload closure is still not run by a test.
