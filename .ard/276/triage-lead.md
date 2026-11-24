---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a per-role `network` setting that defaults to true on every agent stage. In v1 the setting is advisory, except that the worker's test step is enforced under Docker. The work covers config parsing and `config check`, the network fact in the user message and `ARD_NETWORK`, the host-mode warnings, the starter prompts in `internal/roles/`, the PR body's fetched-inputs list, and the proposal text. The scope is concrete enough for the engineer to plan directly. No architect, product or documentation role exists here, so the engineer plan has to cover the config schema and the proposal edits as well.

## Routing
- triage:engineer: lay out the steps across config, executor (docker argv, env, host warning), the claude prompt builder, triage's fixed line listing worker roles, starter prompts, the PR body and the proposal, with argv, env and prompt-content tests.
- worker engineer: Go code, tests, starter prompt copies and documentation changes.

## Open issues
- The change spans several packages, adds a config key on every stage and changes documented security behaviour. With no architect role, the engineer plan and eng-review carry all of that design weight. A maintainer may want to look closely at the config schema and at the risk text in the proposal.
- Aligning the chart's NetworkPolicy comment touches `deploy/`, which is a protected path, so the PR will wait for human approval.
- The live `.ard/` prompt copies are out of scope for agents and need a separate human PR, as the issue already plans.
- The new prompt text changes how future triage leads route work that needs network-only inputs. A maintainer should review those prompt changes as a policy decision.
