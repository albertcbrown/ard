---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
When a done review no longer covers a PR's current head because the code changed, the merger should re-request that review once per head. Today such a PR stays stuck. The change sits mainly in the merger and stale-review gate code (internal/merger, internal/prgate), possibly touches the reviewer's mark-ready sweep, and updates the merger section of the proposal doc. The engineer can plan this directly.

## Routing
- triage:engineer: implementation steps for the once-per-head re-request event, the log hint fix, kill-switch and dry-run handling, and the tests the issue lists.
- worker engineer: code change in the merger and gate packages with unit tests and a doc update.

## Open issues
- The only triage planner configured is engineer, so no architect or documentation planner will review the new event type or the proposal doc change.
- The single reviewer, eng-review, has no when: rule, so it runs on this PR anyway.
