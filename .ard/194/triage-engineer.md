---
role: engineer
status: done
attempt: 1
---
Adds a readiness check to the reviewer loop that marks a worker's draft PR ready for review once every requested review is done at the current head, run after each review close-out and on every tick. It also fixes the proposal and Gate 2 runbook wording. Size M, risk medium.

## Open issues
- The reviewed head is not stored on the PR or in a label, so the plan reads it from the claim event the reviewer records per job and compares code changes since then. A maintainer may prefer a different record.
- The plan fails closed: if the head cannot be determined, the PR is not marked ready.
