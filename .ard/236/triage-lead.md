---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This issue adds enforcement of daily dollar caps per role and per repository at the pool's claim gate. It touches the state, config, pool and serve packages, adds a new budget package, and updates the proposal docs. The owner has already settled the rules, so it goes to the engineer for steps and then to the engineer worker.

## Routing
- triage:engineer: implementation steps across state, config, pool, the new budget package and serve wiring, plus the serve-level, clock-reset and fail-closed tests.
- worker engineer: Go code change with tests and the matching proposal text.

## Open issues
- The change spans several packages, adds a new package and changes the pool's verdicts. That would normally call for an architect, but this repository has no architect, product-manager or documentation-planner triage role. The issue says the design is in the parent issue's implementation plan.
- The scope includes a Layout line in CLAUDE.md. That path is protected, so the pull request will wait for a human approval before merging.
- The only reviewer, eng-review, has no when: rule, so it runs without extra routing.
