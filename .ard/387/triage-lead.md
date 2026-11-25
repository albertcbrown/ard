---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the last part of the network inputs back-route: an end-to-end harness test of the bounce, re-triage, gate and worker path plus its negative cases, and the remaining updates to docs/proposal.md. Part 3 has already merged on main, so the work can start now, and the engineer plans it.

## Routing
- triage:engineer: plan the harness test scenarios (main path through reviewers and the three negative paths, offline) and list which proposal sections still need updating.
- worker engineer: writes the harness test and edits the proposal. No code outside tests and docs should be needed.

## Open issues
- The issue says the live prompt copies under .ard/ stay a human pull request. The worker must not edit .ard/triage/, .ard/workers/ or .ard/reviewers/, and those paths are protected in any case.
- No documentation-planner or architect role exists, so the engineer's plan has to cover the proposal edits.
