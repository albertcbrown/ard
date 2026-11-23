---
role: engineer
status: done
attempt: 1
---
Adds evaluation of reviewer when: rules (changed paths or a triage role that ran) to the worker's reviewer selection, keeping the worker reviewers: override and the lead's extra requests as overrides that always win. The plan has five steps with a test per rule and a proposal update. Size M, risk low to medium.

## Open issues
- The loader already parses when:, but nothing evaluates it today, so the worker requests every reviewer. The plan makes evaluation fail open (a git or store error requests the reviewer).
- Decision made in the plan: paths and triage are combined with OR, and a worker reviewers: list bypasses when: entirely. A maintainer may want to confirm both.
