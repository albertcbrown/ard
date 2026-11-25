---
role: engineer
status: done
attempt: 1
---
Confirms this is a production race: worktree add and remove hold the clone's lock, but fetches take no lock. The plan adds a shared mode to the lock, so fetches take it shared and worktree add, remove and prune take it exclusive, plus tests that force the interleaving and a proposal update. Size M, risk medium because it is concurrency code on the close-out path.

## Open issues
- The repeated race-detector run in the acceptance criteria stays a local check. CI runs only the normal test target, and the plan does not add the repeated run to it.
- The 30 s timeout on worktree add and remove should cover the git command only, not the wait for the lock. Otherwise a long fetch could make them time out.
