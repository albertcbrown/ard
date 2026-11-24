---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This issue adds a per-role network setting to the configuration and passes it through the executor Job and Handle, the agent environment and the agent prompt. Nothing is enforced yet. The design choice is already settled in the issue, and the only triage planner this repository has is engineer, so engineer writes the steps and the engineer worker implements them.

## Routing
- triage:engineer: step-by-step plan across internal/config, internal/configcmd, internal/executor (including local env and host warning), agentjob, triage/reviewer run paths and internal/agent/claude prompt, with tests for parsing, config check output, env/prompt assertions and Prepare/Start rejection.
- worker engineer: Go code change with unit tests across those packages.

## Open issues
- The change reaches several packages and adds a configuration key. No architect, product-manager or documentation-planner role exists, so the triage engineer should also cover documenting the new `network` key and the `config check` output.
- This is part 1 of a larger tracking issue. Actual enforcement comes in a later part, so in this change `network: false` only produces advisory output.
