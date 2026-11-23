---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is part B of the daily budget caps work: when a cap is hit, post a single notice on the status issue and record a capped event. Part A has already merged, so this can start now. The parent issue's plan already settles the design, so the engineer only needs to plan the steps and tests before the engineer worker implements it.

## Routing
- triage:engineer: implementation steps across the state, budget and servecmd packages and the proposal doc. Also the test plan for one notice per capped scope per UTC day (including across a restart), the dry-run ledger write and the comment goldens.
- worker engineer: code change with unit and golden tests, plus the docs update in the same PR.

## Open issues
- The change adds a new stored event type and spans several packages. No architect role exists, so the engineer should follow the design section in the parent issue's plan.
- The dependency on part A is met: it merged in commit 319be49.
