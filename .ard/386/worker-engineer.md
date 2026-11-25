---
role: engineer
status: done
attempt: 1
---
Addresses the review findings on the network inputs back-route (re-arm, persisted round state, inputs gate, per-round approval): adds the missing "waiting for inputs" status-issue count and the tests the plan named for the persisted round state, approval markers, gate paths and lead re-claim.

## Changes
- `internal/triage/inputs.go`, `loop.go`: the gate records one `wait` gate event when it starts waiting for inputs and an `inputs-ready` event when they land. The triage `Store` interface gains `Events`.
- `internal/digest/`: the daily status-issue digest shows a "Waiting for inputs" count (open issues whose newest gate event is the wait); absent when zero.
- `docs/proposal.md`: label state machine and status-issue notes for the round.
- Tests: `internal/closeout/round_test.go` (new), `internal/triage/{inputs,intake,harness,loop}_test.go`, `internal/digest/digest_test.go`.

## Testing
Ran `go vet` and `go test` for closeout, triage, digest, servecmd, roles, docscheck and state; all pass (`make lint test` as a whole timed out here, and `go vet ./...` trips on a parse error in roles testdata, unrelated).
Added: `TestPrepareRoundOpen` (closed and unclosed worker record; persisted round state, plan, labels), `TestPrepareNoRoundNoRearm`, `TestRearmConsumedByFirstEngineerDone`, `TestResumeReplaysRound` (store changed after a crash; Resume uses the stored plan), `TestInputsRedacted`, `TestInputsLostNeedsHuman`, `TestInputsOfAndNetworkRoundCount`; `TestApprovalMarkerPerRound`, `TestRerunBeforeGate`, `TestGateMainReadErrorWaits`, `TestGateInputSymlinkSubmodule`, `TestMainReadOncePerTickAndCacheDropsOnNewSHA`, `TestStatusEventsWaitingForInputs`, `TestRearmedLeadReclaimed` (default and custom intake label modes, edited body), `TestWaitingForInputsCounter`.

## Review findings
- `internal/closeout/prepare.go:251` (round state tests): fixed: tests above.
- `internal/triage/fanin.go:451` (approval per round tests): fixed: `TestApprovalMarkerPerRound` and related.
- `internal/triage/inputs.go:151` (status issue): fixed: gate events plus digest counter and test.
- `internal/triage/inputs.go:108` (gate paths): fixed: tests above.
- `internal/closeout/network.go:164` (InputsOf coverage): fixed: `TestInputsOfAndNetworkRoundCount`.
- `internal/triage/intake.go` (re-claim test): fixed: `TestRearmedLeadReclaimed`. `TestCloserHasNetwork` in servecmd: not changed: the Closer does not expose its deps and a behavioural test needs a full git worktree; the wiring is one line in `reaping.go`.
- `docs/proposal.md`: fixed: added state-machine and status-issue notes (re-arm, persisted state, Clear, gate and approval were already described).

## Open issues
- The status count is derived from gate events, so a closed issue that was waiting is excluded only because the digest lists open issues.
