---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds the daily token check to the pool's claim gate. A job must now pass both the dollar check and the token check. It also adds a once-per-day status-issue notice for each token cap. Both dependencies have landed on main: the dollar gate and its notice loop are in internal/budget, and the token config is in internal/config. The work can start now, and engineer is the only triage role in this repository.

## Routing
- triage:engineer: implementation steps for the token check in internal/budget, wiring it into the existing dollar gate and verdict, the token notice and report, the proposal doc updates, and table tests on a fake clock.
- worker engineer: Go code, tests and doc changes in internal/budget, the pool wiring and docs/proposal.md.

## Open issues
- The issue asks for a human review of the claim-rule table and the fail-closed path. eng-review runs on every pull request, but a maintainer should still review those parts of the pull request.
- No architect or documentation-planner role exists. The engineer's plan has to cover how this fits with the existing dollar gate and notice loop, and the changes to the proposal's Budgets, Configuration, Executors and Reaper sections.
