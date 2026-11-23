---
role: engineer
status: done
attempt: 1
---
Adds daily dollar-cap enforcement at the pool's claim gate: a spend query and index in state, a new budget package, a DeniedBudget verdict, serve wiring and matching proposal text. The plan has seven steps with unit and serve-level tests. Size L, risk medium because it gates every claim and touches a protected path (CLAUDE.md).

## Open issues
- The parent issue's design plan was not available to this planner. Two points should be checked against it: whether a role with no cap of its own is limited only by the repo cap (assumed), and what the `budget` entry in the known loops list registers (assumed to be a small no-claim loop, or the gate alone).
- Spend is keyed by stage and role, because a role name can be shared by a triage role and a worker role.
- The CLAUDE.md Layout line needs a human approval before merge.
