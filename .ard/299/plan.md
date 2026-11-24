# Plan for #299

## Implementation

### Steps
1. `internal/agent/agent.go` (edit `Spec`): add new type `WorkerNetwork struct{ Role string; Network bool }` and new field `WorkerRoles []WorkerNetwork` on `Spec`, commented as "triage stage only; from the resolved config, never from issue text". Reason: the adapter must not read config, so the data travels in the spec.
2. `internal/agentjob/command.go` (edit `AgentCommand`): when `stage == agent.StageTriage`, fill `spec.WorkerRoles` from `cfg.RolesOf(config.KindWorker)` (already in canonical kind/name order), one entry per role with `role.Network`. Doing it here, not in `internal/triage/run.go`, keeps the signature unchanged and makes the line identical for the lead and every other triage role by construction. `run.go` needs no edit because it already calls `AgentCommand` with `agent.StageTriage`.
3. `internal/agent/claude/prompt.go` (edit `userMessage`): after the "Network:" line, when `len(s.WorkerRoles) > 0`, write one fixed line `Worker roles: engineer (network: true), docs (network: false)`, joining entries with ", " and formatting the bool with `%t`. Role names are validated by `^[a-z0-9][a-z0-9-]*$`. Write nothing when the list is empty, so worker, reviewer and scanner messages are unchanged.
4. Tests (see below).

### Test plan
Test command: `go test ./internal/agent/... ./internal/agentjob/... ./internal/triage/...`
- `internal/agent/claude/network_test.go`, `TestUserMessageWorkerRoles`: a spec with `WorkerRoles` of engineer (true) and docs (false) gives a message containing exactly `Worker roles: engineer (network: true), docs (network: false)\n`.
- Same file, empty/nil `WorkerRoles`: the message has no "Worker roles:" line (edge case).
- `internal/agentjob`, new test `TestAgentCommandWorkerRoles` (a new file if no suitable one exists): a config with two worker roles, one `Network: true` and one `false`. The triage stage gets both entries in name order. The worker and review stages get none.
- `internal/triage/snapshot_test.go`, new case beside `TestCommandClaudeArgv`: with two worker roles configured, the argv's user message has the line for two different triage roles (lead and engineer), and the two lines are byte-identical.
- Failure case: a config with no worker roles gives no line and no error.

### Risk
Low. Pure message text built from resolved config. Not security-sensitive, since issue text never reaches it. No protected paths touched (`.ard/triage/` prompts are untouched). `agent.Spec` is an internal interface, and the new field is additive. No migrations or concurrency.

### Size
S. Files: `internal/agent/agent.go`, `internal/agentjob/command.go`, `internal/agent/claude/prompt.go`, `internal/agent/claude/network_test.go`, `internal/agentjob` test file, `internal/triage/snapshot_test.go`.
