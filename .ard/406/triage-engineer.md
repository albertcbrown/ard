---
role: engineer
status: done
attempt: 1
---
Adds a controller wake hook that close-out calls after its label effects, plus a once-a-minute approval and unblock watch with a new intervals.approvals setting (default and minimum 1m). It updates the proposal too. Size L, risk medium because it touches controller scheduling and close-out concurrency.

## Open issues
- Removal of a label cannot be searched on GitHub, so the unblock watch needs an in-memory "seen blocked" set. After a restart it misses removals that happened while ard was down, and the normal tick covers those.
- The worker should check which loop owns a review-round request (workers or reviewfix) before wiring that wake.
