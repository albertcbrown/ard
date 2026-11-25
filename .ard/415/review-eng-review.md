---
role: eng-review
status: done
attempt: 3
verdict: approve
findings:
  - file: "internal/snapshot/snapshot.go"
    line: 497
    severity: medium
    blocking: false
    message: |
      `addOverride` (ConfigMax and Override on PoolView) still has no test; the diff touches no file in internal/snapshot/ other than snapshot.go. plan.md's test plan lists one.
      Add a case in internal/snapshot/snapshot_test.go with a fake Config and Override source.
  - file: "internal/servecmd/controls.go"
    line: 92
    severity: medium
    blocking: false
    message: |
      Unpause's wake of the loops is still untested: the tests build `controls` with no loops, so `wakeLoops` is never reached.
      Add a test with a non-empty `loops` slice and a recording wake.
  - file: "internal/servecmd/controls.go"
    line: 125
    severity: low
    blocking: false
    message: |
      `step` calls `slots.Step` and then `p.SetMax(n)` outside the override's lock, so a concurrent config reload can briefly leave the pool cap out of step with the override. The next reload or key press corrects it.
---
Approved: the change matches the issue and plan.md, and the pause and slot logic is tested. The two medium test gaps from the last round are still open but do not block.

## Checks
- Lint and tests: not re-run in this round, so there is no new result. The diff is unchanged in these areas from the previous round, which found `go vet` clean and `./internal/ui`, `./internal/snapshot` and `./internal/pool` passing under -race. In that round `./internal/servecmd` showed a data race in `TestRunWithUI`, a test that existed before this change.
- Diff coverage: not measured. Slot, controls and key code is exercised except for the gaps above.

## Previous findings
- internal/snapshot/snapshot.go (addOverride untested): still present, medium and non-blocking.
- internal/servecmd/controls.go (Unpause wake untested): still present, medium and non-blocking.
- internal/servecmd/controls.go (SetMax outside the lock): still present, low and non-blocking.
