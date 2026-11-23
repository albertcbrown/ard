---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a signal-driven way for a running ard serve to run one tick of every loop immediately, still gated by the kill switch and the job pool, with a logged wake event and matching documentation. It is a contained controller change, so the engineer plans it and the engineer worker implements it.

## Routing
- triage:engineer: implementation steps for the signal handler, the per-loop wake path, logging, docs updates and tests.
- worker engineer: controller code change with unit tests and doc edits.

## Open issues
- This adds operator-visible behaviour (a new signal) and touches the proposal document; no product-manager, architect or documentation-planner triage role exists in this repository, so a maintainer may want to confirm the signal choice and the documented wording during review.
