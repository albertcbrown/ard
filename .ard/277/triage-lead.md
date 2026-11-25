---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds a way back into the pipeline when a worker without network access needs an input it cannot fetch. The worker reports a new needs-network status, the issue returns to the triage engineer, and then triage, approval, the worker and the reviewers run again, with a cap of one round per issue. The prerequisite work on per-role network access appears to have landed on main (its starter prompts part is the latest commit), so this is routed to the engineer for planning and implementation.

## Routing
- triage:engineer: implementation steps and a test plan that span role-file parsing, close-out outcomes and label phases, the triage input file, the lead re-arm, the gate's inputs check and the per-round approval marker, and the loop guard.
- worker engineer: Go code changes with unit and end-to-end harness tests, the starter prompt copies under internal/roles/, and the proposal doc updates.

## Open issues
- No architect, product-manager or documentation-planner triage role exists in this repository. This change crosses several packages, adds persisted round state to the job record, and changes the label state machine and the role-file contract, so a maintainer may want to review the design before approving.
- The issue estimates about 1,000 lines in one PR. The triage engineer may judge that too large for one plan.
- The dependency check comes from main's commit history only. A maintainer should confirm that the per-role network work, including its prompt part, has fully merged.
- Only the starter prompt copies under internal/roles/ are in scope. The live prompts under .ard/workers/ and .ard/triage/ are protected and need a separate human PR.
