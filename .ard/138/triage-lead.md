---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Close-out should stop a job's plan.md commit from reverting sections that other roles changed on main after the job's base commit. The change belongs in the close-out and commit path (internal/closeout and its callers), with a regression test and a matching update to the proposal document. The engineer plans it and the engineer worker implements it.

## Routing
- triage:engineer: choose between replacing only the job's own section on the current main copy and failing closed to needs-human, then write the implementation steps and the regression test plan.
- worker engineer: code change in close-out/commit handling, a test where another role's section changes on main, and the proposal doc update.

## Open issues
- Only the engineer triage role exists, so no architect or documentation-planner section will be written. The change touches the close-out flow and the documented ownership rules for plan.md, so a maintainer may want to confirm the chosen approach during review.
