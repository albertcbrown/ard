---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The earlier blocker is gone: the owner dropped the Gate 3 dependency, and part A has now logged several days of readings. This is part B of limits-aware scheduling. It adds a `limits:` config block with validation, a gating policy with paced weekly caps, pool candidate fields, the one-job rule in serve, and a docs update. The scope and acceptance tests are spelled out, so it goes to the engineer for planning and implementation.

## Routing
- triage:engineer: implementation steps across internal/config, internal/limits, internal/pool, internal/servecmd and docs/proposal.md. That includes the day-of-window calculation, config validation and the table tests with a fake Source and a fake clock. The repository has no architect role, so the engineer also covers the cross-package design.
- worker engineer: code, tests and docs change in Go, run with `make lint test`.

## Open issues
- The owner wants to review the PR by hand before it merges, because it changes the pool. internal/pool is not in merge.protected_paths, so the merger will not hold it automatically. A maintainer must hold the PR by hand.
- The issue body was edited after it was first admitted, which paused triage. This run uses the current body, including the owner's 2026-11-26 decisions that replace the flat weekly threshold with the per-day list.
