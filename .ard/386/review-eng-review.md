---
role: eng-review
status: done
attempt: 2
verdict: approve
findings:
  - file: "internal/gitx/local.go"
    line: 573
    severity: medium
    blocking: false
    message: |
      EntryAt (the ls-tree parser behind the inputs gate) has no test in gitx.
      Its bad-path guard, the missing-path case, the blob/symlink/submodule parsing and the size field are only exercised through fakes in triage. Add a test against a temporary repository.
  - file: "internal/servecmd/reaping.go"
    line: 68
    severity: low
    blocking: false
    message: |
      plan.md's TestCloserHasNetwork was not written, so nothing pins that the bounce stays switched on in servecmd. Add a small test that wiring.closer() builds a Closer with Deps.Network set.
---
The previous blocking findings are fixed: the persisted round state, resume replay, approval per round, the digest "waiting for inputs" counter and the other missing tests now exist and pass. Two small gaps remain (EntryAt and the servecmd wiring have no test), and neither blocks.

## Checks
- Lint: `make lint` was not re-run. `gofmt -l internal` is clean on the changed files; its only output is a parse error in testdata (`internal/roles/testdata/docs/skips/src/_x.go`), which the change did not touch.
- Tests with coverage: `go test -cover` on closeout (90.1%), triage (87.4%), digest (91.7%), servecmd (79.3%), gitx (78.8%), roles and docscheck all pass. `EntryAt` has no direct test.

## Previous findings
- internal/closeout/prepare.go (persisted round state tests): resolved
- internal/triage/fanin.go (approval per round tests): resolved
- internal/triage/inputs.go (status issue waiting counter): resolved
- internal/triage/inputs.go (gate paths: single SHA read, cache, read error): resolved
- internal/closeout/network.go (InputsOf and NetworkRoundCount tests): resolved
- internal/triage/intake.go (TestRearmedLeadReclaimed): resolved; TestCloserHasNetwork is still missing (see the low finding)
- docs/proposal.md (docs depth): resolved
