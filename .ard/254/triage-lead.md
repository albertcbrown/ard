---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This narrows the generic-assignment rule in the log secret scan so that Go selector or identifier values and plain numeric counts stop counting as secrets, while quoted and opaque values are still flagged. The change stays inside the internal/redact package (rule definition and table tests), so the engineer plan is enough.

## Routing
- triage:engineer: work out the matching change in the generic-assignment rule and a table-test plan that covers the reported false positives and keeps the true positives.
- worker engineer: change the rule and add the tests in internal/redact.

## Open issues
- This is a security-sensitive scanner. The plan and review should confirm that the new exclusions cannot let real secrets through, for example identifier-shaped or all-digit tokens.
