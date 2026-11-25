---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the first slice of the serve command center: a new in-process snapshot package with no UI. It reads from the pool, limits, state and controller packages and adds per-role tracking of refused pool requests. It is a clear, bounded code change. The only triage role here is engineer, so the engineer plans it and the engineer worker implements it.

## Routing
- triage:engineer: implementation steps for the new snapshot package, the pool's refusal counters (grant decisions must not change), the slog tee ring buffer and the cached issue usage, plus the table-test plan with fakes and a GitHub fake that asserts zero requests.
- worker engineer: code, tests and the docs/layout.md entry.

## Open issues
- The design this slice follows is in the parent issue, 407, not in this issue. The planner and worker should read it so the snapshot fields match the reviewed design.
- The change adds a new package and touches several existing packages (pool, limits, state, controller, servecmd logging). This repository has no architect role, so the engineer plan has to settle the package boundaries itself. A maintainer may want to check that part of the plan.
- Adding refusal counters to the pool is the riskiest part. The plan and the review should confirm that the pool's grant behaviour is unchanged.
