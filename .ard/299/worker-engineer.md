---
role: engineer
status: done
attempt: 1
---
Adds a fixed "Worker roles: name (network: bool), ..." line to every triage job's user message, built from the resolved worker roles, so triage knows each worker's network setting.

## Changes
- `internal/agent/agent.go`: new `WorkerNetwork` type and `Spec.WorkerRoles` field.
- `internal/agentjob/command.go`: for the triage stage, fills `WorkerRoles` from the config's worker roles. This makes the line identical for all triage roles. `internal/triage/run.go` needs no edit.
- `internal/agent/claude/prompt.go`: writes the line only when the list is non-empty.
- Tests: `network_test.go` (line and empty case), new `internal/agentjob/command_test.go` (triage gets entries, worker/review none, no workers no error), `internal/triage/snapshot_test.go` (two worker roles, lead and architect lines identical).

## Testing
`go test ./internal/agent/... ./internal/agentjob/... ./internal/triage/...` passes; `go vet` clean on the changed packages.

## Open issues
- The triage prompts under `.ard/triage/` may want to refer to the line; a person must edit them.
