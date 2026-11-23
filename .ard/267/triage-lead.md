---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a new controller loop that polls ard's own pull requests, records human merges, and notifies on closed-unmerged PRs, long-pending checks and red drafts. It also moves check evaluation out of the merger into a shared package. The engineer is the only triage planner configured, so it plans the whole change and the engineer worker implements it.

## Routing
- triage:engineer: implementation steps across config, state (new table and migration), prgate, merger, a new prwatch package and serve wiring, plus the test plan and the split decision.
- worker engineer: Go code change with table-driven tests and doc updates.

## Open issues
- This is a large change (the issue estimates about 1,200 lines) that touches several packages, the stored schema and configuration. No architect, product-manager or documentation-planner role exists, so the engineer's plan has to cover those concerns. A maintainer may want to confirm the two-part split the issue offers before work starts.
- The change edits CLAUDE.md, which is a protected path, so the pull request will wait for a human approval before it can merge.
- Moving the merger's check functions should not change behaviour. eng-review should confirm that the merger's existing check tests move over without edits.
