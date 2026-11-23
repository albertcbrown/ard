---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Routing the deterministic docs scanner (PR A of the plan) to the engineer. It is a self-contained new starter role under internal/roles with golden tests, proposal updates and a one-line CLAUDE.md change. The LLM doc-drift half (PR B) cannot start yet and stays out of this run.

## Routing
- triage:engineer: confirm the implementation steps and test plan for the deterministic `docs` scanner (scan script, stdlib-only Go program, starter config override, roles and initcmd tests, proposal text).
- worker engineer: implements PR A only, as a code, test and docs change.

## Open issues
- PR B (the `docs-drift` LLM scanner) is blocked on the LLM scanner engine, tracked as issue 204. It needs its own run after that lands and should not be part of this worker's change.
- PR A edits CLAUDE.md, which is a protected path, so the merge waits for a human approval.
- Human-only follow-ups stay outside the agent's reach: copying the role into ard's own .ard/scanners/docs, adding the override to .ard/config.yaml, extending the deploy/test-repo.sh starter check, and syncing the new scanner label.
- No architect, product-manager or documentation-planner triage roles exist in this repository, so the engineer plan has to cover the proposal and configuration documentation itself.
