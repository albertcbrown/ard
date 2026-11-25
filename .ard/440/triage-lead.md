---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A flaky test in the committer package cancels its context on a wall-clock timer, so under load it can kill a git subprocess before the transient backoff starts. The fix is limited to the test and its test double in internal/committer. There is no change to production behaviour, so only the engineer planner is needed.

## Routing
- triage:engineer: plan how the test double should trigger cancellation, and how to check that the test still fails if the backoff ignores cancellation.
- worker engineer: test-only change in internal/committer, verified with repeated race runs.

## Open issues
None.
