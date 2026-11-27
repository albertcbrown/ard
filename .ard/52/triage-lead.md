---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The work that blocked this issue has now landed. The LLM scanner engine from #204 is on main: `AgentEngine`, `agentjob.ScannerCommand` and the `$ARD_OUT` output directory are all in the tree. What is left is the starter role `scanners/architecture` (the plan's PR 3). It is a narrow, well-fenced change under `internal/roles` and `internal/initcmd`, so it goes to the engineer planner and the engineer worker.

## Routing
- triage:engineer: confirm the plan against main as it is now, then write the steps. That covers the starter prompt under `internal/roles/scanners/architecture`, its entry in `internal/roles/config.yaml`, the role list in `internal/roles/roles.go` and `internal/initcmd`, and the role tests. The tests check that the prompt's worked examples pass the fingerprint grammar check.
- worker engineer: add the role files, the config entry and the tests.

## Open issues
- No product-manager, architect, designer or documentation-planner triage role exists in this repository, so only the engineer plans this. The change adds a role to the starter set that `ard init` proposes to every adopter. A maintainer should read the shipped prompt before merging. `internal/roles/` is not in `merge.protected_paths`, so nothing forces that review.
- The rollout has to be done by a human. Adding the scanner to this repository's own `.ard/scanners/` and `.ard/config.yaml`, after a dry-run smoke run, touches protected paths that agents may not change.
- The plan calls for updates to the Configuration section of `docs/proposal.md` (the starter list). The engineer planner should check which of those #204 already made.
