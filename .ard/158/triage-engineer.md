---
role: engineer
status: done
attempt: 1
---
Excludes the agent auth mode variable by exact name in the redactor's environment collection, so the spurious short-value warning stops while the real credentials stay redacted. The plan has three steps and one new test. Size S, risk low.

## Open issues
- Other non-secret variables whose names contain AUTH would still trigger the same warning; the plan keeps the exclusion to a single exact name on purpose.
