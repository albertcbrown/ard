---
role: engineer
status: done
attempt: 1
---
Adds a diagnosis step and a small hardening change: git's auto gc and maintenance are turned off for every gitx command, with an optional single retry of worktree add, plus tests that run add/remove cycles beside fetches. Size S to M, risk low to medium.

## Open issues
- Per-repository serialization of worktree add, remove and prune already exists (internal/wtlock, used by gitx and the local executor), so the issue's proposed fix is already in place. The cause is therefore unconfirmed. Thirty local race-enabled runs of the stress test passed, and the plan's leading suspect is a detached background git gc prune.
- The full 50-run check takes roughly 70 s locally, so the worker should be able to run it in full.
