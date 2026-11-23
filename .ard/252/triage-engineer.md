---
role: engineer
status: done
attempt: 1
---
Makes the merger remove a role's done review label once per head when the head moved with code changes, record a new event so it never repeats, and log the re-request. The log hint is corrected, main-only moves are left alone, and dry-run writes only to the ledger. Size M, risk low to medium.

## Open issues
- The worker must confirm in the reviewer's claim code that removing the done label alone makes the role re-run; the plan says what to add if it does not.
- The pause switch needs no new code because the merger loop is already skipped while paused.
