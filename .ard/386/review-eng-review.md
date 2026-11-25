---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/closeout/prepare.go"
    line: 251
    severity: high
    blocking: true
    message: |
      The persisted round state has no test: plan.md's TestPrepareRoundOpen and TestResumeReplaysRound were not written.
      This is the part the issue says needs architect review (Prepare computes Network.Round/Done and Rerun, ResumeJob must replay the stored plan).
      Add tests that run Prepare for an engineer done with an open round and for the lead after a re-arm, assert j.Network is persisted, then change the store and check ResumeJob uses the stored plan. Also TestRoundOpenUnclosedWorker (worker record in phase prepared) and the inputsLost needs-human path (prepare.go:171) and TestInputsRedacted.
  - file: "internal/triage/fanin.go"
    line: 451
    severity: high
    blocking: true
    message: |
      The approval-per-round behaviour has no test (plan: TestApprovalMarkerPerRound, TestRerunBeforeGate, and the gate case where inputs land and a fresh "ready for approval" posts).
      Add tests that an older approval is ignored after a new engineer :done, round 0 keeps the old marker, the round suffix is used for n>0, an approval given while inputs were missing counts once they land, and that a network round with RequireApproval=false and an unclosed worker record still asks.
  - file: "internal/triage/inputs.go"
    line: 151
    severity: high
    blocking: true
    message: |
      The status issue does not show "waiting for inputs". The issue scope and plan step 7 ask for it (servecmd/digest.go StatusIssue); digest.go is not touched and nothing outside the gate comment mentions it. Add the counter and a test.
  - file: "internal/triage/inputs.go"
    line: 108
    severity: medium
    blocking: false
    message: |
      Untested gate paths from plan.md: the per-tick single MainSHA call and the SHA-keyed cache dropping on a new SHA (mainLookup), a MainSHA/MainEntry error leaving the issue waiting (gateInputs, 80% covered), and symlink/submodule entries through the gate (only decideInputs is table-tested). Add them.
  - file: "internal/closeout/network.go"
    line: 164
    severity: medium
    blocking: false
    message: |
      InputsOf and NetworkRoundCount have no test in this package (0% coverage from closeout tests). Add a case that the newest engineer done record's inputs are returned and that none gives ok=false.
  - file: "internal/triage/intake.go"
    severity: medium
    blocking: false
    message: |
      plan.md's TestRearmedLeadReclaimed (an App-added ard:pending re-admits the lead, with the admission hash check and custom intake label mode) and TestCloserHasNetwork in servecmd were not written. The bounce is switched on in servecmd/reaping.go, so the re-claim of the re-armed lead is the live path and should be pinned by a test.
  - file: "docs/proposal.md"
    severity: low
    message: |
      Only 4 lines changed. Plan step 11 asks for re-arm, persisted round state, Clear, the inputs gate, per-round approval, approval-while-waiting and the label state machine notes.
---
The core logic (rounds keyed off the Plan, Clear in request, re-arm in Build, the inputs gate, forced approval) matches the plan and the existing tests pass. But several plan test cases for the persisted round state and approval are missing, and the status issue "waiting for inputs" is not done, so this needs changes.

## Checks
- Lint: `make lint` runs `go vet ./...`; it reported only a parse error in `internal/roles/testdata/docs/skips/src/_x.go` (testdata, not in the change). It was cut short while downloading modules, so the rest did not run.
- Tests with coverage: `go test -cover ./internal/closeout ./internal/triage` pass (88.8% and 86.3%). `InputsOf` and `NetworkRoundCount` are not executed by closeout's own tests. `servecmd` and `roles` tests were not run (time budget).
