---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds owner-set priority labels (P0 to P3) that order candidates within each pool class and order the merger's queue. The design is already detailed and the work it was waiting on has landed: the slot wake-up change is on main (PR #403) and so is the CLAUDE.md invariant wording (PR #397). It goes to the engineer planner and then the engineer worker.

## Routing
- triage:engineer: turn the design into steps for the proposed two-part split (labels, pool ranking, loop wiring and the extra searches first; merger ordering and the starvation-warning fix second), building on the pool code as it now stands after the wake-up change, with the table tests the acceptance criteria list.
- worker engineer: Go changes in internal/labels, internal/pool, the triage, worker, reviewer, scanner and merger loops, plus the proposal docs.

## Open issues
- The issue covers about 900 changed lines and proposes two PRs, the second depending on the first. A maintainer may want to file part 2 as its own issue so that each PR stays reviewable.
- No product-manager, architect or documentation-planner triage roles exist here. The issue says those reviews were already folded into the design, so only the engineer plans it.
- The CLAUDE.md wording is already on main, so the worker must not touch CLAUDE.md. It is a protected path.
