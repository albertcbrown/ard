---
role: engineer
status: done
attempt: 1
---
The triage fan-in gate now checks that the plan file exists on main before it asks for approval or starts a worker. If the file is missing, the gate posts one comment and sets needs-human.

## Changes
- `internal/triage/loop.go`: optional `Deps.PlanExists` (nil skips the check).
- `internal/triage/fanin.go`: for a ready verdict the gate calls `PlanExists`. A missing plan goes to `gateHuman` with reason `no-plan` and a fixed-text comment. A check error is returned and retried next tick, with nothing posted.
- `internal/servecmd/triage.go`: `planExists` fetches main into a private ref and reads `.ard/<n>/plan.md` with `FileAt`.
- `internal/triage/harness_test.go`, `fanin_test.go`: the harness fake defaults to "plan exists". New `TestGateNoPlan` covers approval on and off, a check error, and a waiting role that skips the check.
- `docs/proposal.md`, `docs/runbooks/gate-2.md`: describe the check.

## Testing
`go test ./internal/triage/... ./internal/servecmd/...` passes. I did not add a unit test for the `planExists` helper, and the e2e fixture was not changed. I did not run the full `make test`.

## Open issues
- A lead-only triage run never writes a plan.md, so it now stops at needs-human, as the plan notes.
- The gate now does one git fetch of main per gated issue per tick.
