---
role: engineer
status: done
attempt: 1
---
Adds a per-role `network` setting (default true) and carries it from config through the executor Job and Handle to the agent environment and prompt. Nothing is enforced; `network: false` is advisory.

## Changes
- `internal/config`: `Role.Network`, a `network` override on every agent kind (non-bool rejected), and a rendered value (`true`, `false (advisory)`, `false (advisory, host)`) with an updated golden file.
- `internal/configcmd` and `local/hostwarn.go`: `config check` warns for host mode, untrusted input and `network: true`, naming the App private key file.
- `internal/executor`: `Network` type and constants, a field on `Job` and `Handle`, `Validate` rejects unknown values (so Prepare does), Prepare copies it.
- `internal/executor/local`: `ARD_NETWORK` is 0 unless `NetworkOn`; Start rejects `NetworkNone` with a credential in `cmd.Env`; host jobs log "network: false not enforced" (also for trusted-input host jobs).
- `agentjob.ExecJob` (triage, review), the worker agent step and the scanner engine set `NetworkAdvisory` for `network: false`. The worker test step stays on.
- `agent.Spec.Network` and the constant "Network: available./unavailable." line in the Claude user message.
- `docs/proposal.md`: documents the key.
- Tests: config, configcmd, validate, local env/Start check, prompt. Existing host-warning and env tests were updated for the new warning and `ARD_NETWORK`.

## Testing
`go vet ./...` and `go test ./...` pass. I added no dedicated tests for the hostwarn log line, `ExecJob`/worker Network assignment or the docker argv; those paths are covered only by the shared `ardEnv` test.

## Open issues
- yaml.v3 accepts a quoted "no" as a bool, as it does for `autoclose`; the non-bool test uses other values.
- `gofmt -l` reports a pre-existing broken fixture under `internal/roles/testdata`, unrelated.
