---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the first part of per-role token caps. It adds a token ledger: a token-count config type with a per-role tokens_per_day setting, per-role token accounting in the state store, and a worst-case charge in the reaper for agent jobs that end without a result. The scope is well defined and has no gating, so it goes to the engineer for planning and implementation.

## Routing
- triage:engineer: implementation steps and a test plan across internal/config, internal/state and internal/reaper, including fake-clock day-boundary, restart and idempotent-charge tests.
- worker engineer: Go code change with unit tests.

## Open issues
- The change spans three packages, adds a configuration key and changes stored data. That would normally go to an architect, but this repository has no architect triage role. The issue points to an existing design in the parent tracking issue, and the engineer plan should follow it.
- The new tokens_per_day key is user-visible configuration. There is no documentation-planner role, so the engineer plan should cover any docs updates, such as the configuration reference.
