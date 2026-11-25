---
role: engineer
status: done
attempt: 1
---
Adds a new protected-branch push error class, re-fetches main after it on the main lane, and retries within the existing push budget only when main moved. Otherwise it fails at once with a distinct protection cause and a comment pointing at the ruleset bypass. Size M, risk medium because it changes the retry loop every close-out uses.

## Open issues
- A maintainer should confirm the rule that a protection rejection is retried only when main moved since the lease.
- The plan names the closeout cause `push_protected`; the issue called it CauseProtected, so the string value is a choice for the worker.
