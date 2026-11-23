---
role: lead
status: blocked
attempt: 1
---
This is part B of the limits-aware scheduling work: config thresholds, a gating policy and the one-job rule in the pool and serve loop. Part A (#238) landed today, but the issue also waits for Gate 3 and a few days of real usage-window readings from part A. Neither is met yet, so planning should not start.

## Routing
None.

## Open issues
- Waiting on Gate 3 and a few days of real readings from part A, which merged on 2026-11-23. A maintainer should re-queue this issue once both are met.
- When it is re-queued, expect triage:engineer and worker engineer. The change touches config, a new policy in internal/limits, pool candidate fields, servecmd and docs/proposal.md. Only the engineer triage role exists here, so it will need to cover the cross-package design too.
- The issue asks for human review before merge because it changes the pool. internal/pool is not in protected_paths, so a maintainer must enforce that review by hand.
