---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the first half of the conflict-rounds work. It makes review-fix aware of why a round runs and lets the merger request a conflict round. A switch keeps the feature off, so production behaviour stays the same. The scope is clear and has no dependencies, so the engineer plans it and the engineer worker implements it.

## Routing
- triage:engineer: write implementation steps and a test plan for the round-reason changes in reviewfix, the merger's conflict path and Rounds dependency, the off-by-default switch and the proposal doc update.
- worker engineer: Go code changes in reviewfix, merger and servecmd, with unit tests and a docs update.

## Open issues
- The change touches several packages and possibly the configuration schema, but this repository has no architect or documentation-planner triage role. The engineer's plan has to cover those concerns.
- The switch can be a new configuration key or the merger's Rounds left nil in servecmd. If the plan adds a key, it should change only the schema and its defaults. The worker must not edit .ard/config.yaml, which is a protected path.
