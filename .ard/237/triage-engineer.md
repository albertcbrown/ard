---
role: engineer
status: done
attempt: 1
---
Adds a budget notice loop that posts one status-issue comment per capped scope per UTC day, records a budget_capped event (also the restart-proof marker), and updates the proposal. The plan has seven steps and tests for dedup, restart, dry-run and goldens. Size M, risk low to medium.

## Open issues
- The parent plan for the budget work is not in the repository, so the design follows the issue and the merged part A code.
- A crash between posting the comment and recording the event can repeat one notice; the plan accepts this over losing a notice.
