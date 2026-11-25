---
role: engineer
status: done
attempt: 1
---
Adds a daily token check to the budget gate in internal/budget, next to the dollar check, and a once-per-day token notice on the status issue. It also adds a token report and updates the proposal. The plan has six steps and tests for the rule, both caps, fail-closed reads, the UTC reset and the notice. Size M to L, risk medium because it sits on every job's claim path.

## Open issues
- A maintainer should review the claim-rule table and the fail-closed path, as the issue asks.
- The existing dollar notice marker differs from the requested token marker. The plan keeps the dollar marker unchanged and adds the token marker alongside it.
