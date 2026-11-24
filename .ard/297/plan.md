# Plan for #297

## Implementation

### Steps
1. `internal/executor/executor.go` (new): add `type Network string` with `NetworkOn` (""), `NetworkAdvisory` ("advisory"), `NetworkNone` ("none"). Add `Network Network` to `Job` (next to `ReadOnly`) and to `Handle` (comment: copied from Job by Prepare). Never add it to `Command`.
2. `internal/executor/validate.go`: in `Job.Validate`, add a case rejecting any `Network` other than the three constants with `bad("network %q", j.Network)`; Prepare already calls Validate, so this is the Prepare rejection.
3. `internal/executor/local/local.go` (~188): set `Network: j.Network` in the `executor.Handle` literal in `Prepare`.
4. `internal/executor/local/proc_unix.go` `Start`: after `checkEnv`, if `h.Network == executor.NetworkNone && len(c.Env) > 0` return a wrapped `executor.ErrForbiddenEnv` (a credential in `cmd.Env` needs the model API). Put it in a new helper `checkNetwork(h, c)` in `env.go`.
5. `internal/executor/local/env.go` `ardEnv`: append `ARD_NETWORK=0` when `h.Network != executor.NetworkOn`, else `ARD_NETWORK=1`. Both host (`hostEnv`) and docker (`runArgv`, docker.go ~81) already use `ardEnv`. Check `checkFixed` and any allow list does not reject `ARD_NETWORK` (it is output only, never read back).
6. `internal/executor/local/hostwarn.go` `warnHostJob`: when `h.Network != executor.NetworkOn`, also log a WARN with the text "network: false not enforced" (job, issue, stage, role). Note `Start` only calls `warnHostJob` when `hostUntrusted(h)`; add a separate call in `proc_unix.go` for `h.Kind == "host" && h.Network != NetworkOn` that logs just the network line, so a trusted-input host job also logs it. Do not double-log the untrusted WARN.
7. `internal/config/types.go`: add `Network bool` to `Role` (comment: default true).  `internal/config/load.go`: add `network *bool` to `rawOverride`; add `"network": true` to `commonOverrideKeys`; decode with `decodeValue(value, cfgPath, fkey, &ov.network)` in the key switch next to `autoclose` (~297, non-bool is rejected by decodeValue); set `role.Network = true` in `applyRoleDefaults`; apply `if ov.network != nil { role.Network = *ov.network }` next to the autoclose block (~415).
8. `internal/config/render.go`: add `Network bool \`yaml:"network"\`` to `roleYAML` (not omitempty, so false shows) and set it in `render`. Also render the advisory suffix: the YAML value must stay a bool-compatible field, so add a string field instead: `Network string \`yaml:"network"\`` set to `"true"`, `"false (advisory)"` or, when the role runs host mode (`role.Dockerfile == ""` and `r.Executor != "k8s"`), `"false (advisory, host)"`. Update `internal/config/testdata/resolved.golden`.
9. `internal/configcmd/config.go` `Check`: for each `res.HostUntrusted()` role with `r.Network` true, print to stderr (and log) a warning that the role reads untrusted input in host mode with network on, so the agent can send out anything it reaches, naming the GitHub App private key file (`ARD_GITHUB_PRIVATE_KEY_FILE`) as reachable. Add `local.HostNetworkText(kind, name)` in `hostwarn.go` for the text.
10. `internal/agent/agent.go`: add `Network bool` to `Spec` (comment: from the role's network setting; never from issue text). `internal/agentjob/command.go` `AgentCommand`: set `Network: role.Network`. Zero-value risk: callers that build a Spec by hand get "unavailable"; update the tests that construct Spec.
11. `internal/agent/claude/prompt.go` `userMessage`: append the constant line "Network: available." or "Network: unavailable." before the final line.
12. `internal/agentjob/command.go` `ExecJob`: `if !role.Network { j.Network = executor.NetworkAdvisory }`. This covers triage (`triage/run.go` ~108) and review (`reviewer/run.go` ~158).
13. `internal/worker/run.go` `execJob` (~314): set `ej.Network = executor.NetworkAdvisory` when `!role.Network`, but only for the agent step (call at 239); leave the test step (call at 629) on `NetworkOn` since `make test` may need modules. Do this by a small `agentOnly` argument or by setting the field after the call at 239.
14. `internal/scanner/engine_exec.go` (~119): set `ej.Network = executor.NetworkAdvisory` when `!role.Network` (LLM scanners; deterministic scanners get it too, harmless).
15. Docs: `docs/proposal.md` Configuration section: document `network` (default true, valid on every agent kind, advisory only in this part, the `ARD_NETWORK` variable and the prompt line, the config check suffixes and warning).

### Test plan
Command: `make test` (`go test ./...`), plus `make lint`.
- `internal/config/load_test.go`: `TestNetworkDefaultTrue` (no override -> true on every kind); `TestNetworkOverrideFalse` (`network: false` on a scanner, triage, worker and reviewer role -> false); `TestNetworkRejectsNonBool` (`network: "no"` and `network: 3` -> error naming `overrides.<key>.network`).
- `internal/config/render_test.go` and golden: `network` shows `true`, `false (advisory)` (Dockerfile role) and `false (advisory, host)` (no Dockerfile); k8s executor never gets `host`.
- `internal/configcmd/config_test.go`: host + untrusted + `network: true` prints a warning containing "ARD_GITHUB_PRIVATE_KEY_FILE"; `network: false` or a role with a Dockerfile prints none; trusted-input deterministic scanner prints none.
- `internal/executor/validate_test.go`: `Network: "bogus"` -> `ErrInvalidJob`; the three valid values pass.
- `internal/executor/local/local_test.go`: Prepare copies `Network` into `Handle`; Start with `NetworkNone` and one `Env` credential -> `ErrForbiddenEnv`; `NetworkNone` with empty Env starts.
- `internal/executor/local/misc_test.go` (or env test): `ardEnv` gives `ARD_NETWORK=1` for `NetworkOn`, `0` for Advisory and None; docker argv contains `-e ARD_NETWORK=0` for an advisory job.
- `internal/executor/local/hostwarn_test.go`: host job with `NetworkAdvisory` logs "network: false not enforced"; `NetworkOn` does not; docker job does not.
- `internal/agent/claude/prompt_test.go` (or existing claude test): `Network: true` -> "Network: available."; false -> "Network: unavailable."; the line does not change with untrusted file names.
- `internal/agentjob/job_test.go`: `ExecJob` sets `NetworkAdvisory` only for a `network: false` role; `AgentCommand` passes `Spec.Network`.
- `internal/worker` test: agent step job has Advisory for a false role, test step job stays `NetworkOn`.
- Edge: existing tests that compare whole env lists or whole user messages need the new line added.

### Risk
Low to medium. Nothing is enforced. Touches the public executor interface (`Job`, `Handle`; handles are stored, and an old stored handle decodes to the zero value `NetworkOn`, which is safe). Security-sensitive in that the value must never come from the agent: it is set only from config via `ExecJob`, worker and scanner builders. `.ard/config.yaml` is a protected path but this change does not edit it. Changes the agent prompt and env for every job (new line, new variable).

### Size
M to L (roughly 300 changed lines including tests and the golden file). Files: `internal/executor/executor.go`, `validate.go`, `local/local.go`, `local/env.go`, `local/proc_unix.go`, `local/hostwarn.go`, `internal/config/types.go`, `load.go`, `render.go`, `testdata/resolved.golden`, `internal/configcmd/config.go`, `internal/agent/agent.go`, `internal/agent/claude/prompt.go`, `internal/agentjob/command.go`, `internal/worker/run.go`, `internal/scanner/engine_exec.go`, `docs/proposal.md`, and the test files above.
