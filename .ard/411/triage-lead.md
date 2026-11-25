---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A committer stress test fails now and then because a main-lane fetch reads a branch lane's worktree admin directory while that worktree is half-added or half-removed, and `ard serve` could hit the same race in production. This is a clear code change: confirm how fetches and worktree add/remove are locked against each other on a shared clone, serialise them, and add a test that forces the interleaving. It goes to the engineer for planning and implementation.

## Routing
- triage:engineer: check the current locking in the committer's branch and main lanes, gitx and wtlock, then plan how to serialise fetches with worktree add/remove/prune, the test that forces the interleaving, and the update to the committer concurrency rule in the proposal.
- worker engineer: Go change in the committer and gitx code, with tests, checked by make lint test.

## Open issues
- Before the fix, the plan should confirm that this is a production race and not only a quirk of the test setup. The fix should be a lock; a retry on this one error is only a backstop.
- The repeated race-detector run in the acceptance criteria takes a long time. The plan should say whether it runs in CI or only locally.
