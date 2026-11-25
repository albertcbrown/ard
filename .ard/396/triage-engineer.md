---
role: engineer
status: done
attempt: 1
---
Adds the four priority labels with a `labels.Priority` helper, ranks candidates by priority within each pool class, adds an extra P0/P1 search so high-priority items beyond the oldest 50 are found, and orders the merger by priority. Size L (about 900 lines), risk medium because it changes ordering in the shared pool and the merger.

## Open issues
- The plan is written as one run in a safe build order, but it can be split into two PRs at steps 4 and 10 as the issue proposes.
- Several constructors of `pool.Candidate` must set `Priority` explicitly, since the zero value would rank as P0.
- I wrote the plan from the issue and a partial code read. The worker should confirm the exact worker and reviewer loop file names and the merger's branch-update function before editing.
- CLAUDE.md is protected and already updated, so the worker must leave it alone.
