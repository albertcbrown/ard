---
role: engineer
status: done
attempt: 1
---
Replans the network inputs back-route part 3 with the operator's architect-review changes: a request-phase label clear, plan-based round detection that fails closed, redacted inputs, hardened path and main reads, and the bounce switch in the same change. Size L, risk high because it changes the label state machine, stored job state and approval logic.

## Open issues
- The live engineer prompt under .ard/triage/ is protected, so a human must update it to match the starter copy.
- If the change grows past the estimate, the plan names a two-part split.
- The exact status-issue renderer for "waiting for inputs" is left for the worker to locate.
