---
role: engineer
status: done
attempt: 1
---
Revises the plan to the owner decision: a token-count config type with a per-role daily token setting, per-role token totals in the state store, and a "tokens not reported" mark in the close-out comment and cost-table row. No charge is recorded and there is no migration. Size M, risk low to medium.

## Open issues
- The cost table is parsed strictly, so its row pattern must change to accept the new wording; the plan covers this with tests that still reject edited blocks.
- The worker should confirm that a resumed close-out keeps the stored job record, so the mark is carried over and shown once.
