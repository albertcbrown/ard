---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A flaky committer stress test points to a race in git worktree admin-directory handling between per-branch lanes. This is an internal concurrency fix in the committer and its git helper, with a reproducing stress run as the acceptance check, so only the engineer needs to plan it.

## Routing
- triage:engineer: choose between per-repository serialization of worktree add, remove and prune and unique admin names, and lay out the steps and the race-enabled stress test run.
- worker engineer: code change in the committer and git helper packages, plus test verification.

## Open issues
- The acceptance check runs the stress test 50 times under the race detector, which may be slow in the worker's time budget; the plan should say whether the fix runs it in full or with a smaller count.
