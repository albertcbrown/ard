---
role: engineer
status: done
attempt: 1
---
Adds a `limits:` config block with validation, a new limits policy that gates agent jobs on the 5-hour thresholds and the paced weekly list, pool candidate fields with a one-agent-job rule above the 5-hour threshold, serve wiring and a docs update. Size L, risk medium because it changes pool admission for every agent job.

## Open issues
- The issue mentions a "limits loop" with `Claims() == false`, but no such loop exists. The plan gates through the pool's existing limit callback and adds no loop. A maintainer should say if a loop was really intended.
- The one-job rule needs a new pool hook (`Options.Annotate`) so the five loop call sites stay unchanged. This is a design choice the owner may want to check in review.
- The owner wants to review the PR by hand before it merges. `internal/pool` is not a protected path, so a maintainer must hold it.
