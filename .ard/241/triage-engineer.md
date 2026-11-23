---
role: engineer
status: done
attempt: 1
---
Adds a token-count config type and a tokens_per_day setting, a token ledger in the state store (per-role daily tokens, per-role worst case, idempotent charge) and a worst-case charge in the reaper for agent jobs that end without reported tokens. The plan has seven steps with tests for parsing, the UTC day boundary, restart and single charging. Size M, risk medium because of a new migration and the close-out path.

## Open issues
- The plan adds a docs note in the proposal's configuration section; no gating is described or implemented.
- Worst-case and charge details (charges never feed the worst case; the worst case uses all history) are the engineer's reading of the issue, since the parent design text was not available here. A maintainer should check them against it.
