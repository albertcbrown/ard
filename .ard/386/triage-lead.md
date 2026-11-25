---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Part 3 of the network inputs back-route: re-arm the lead and re-run triage in a network round, persist the round state in the job record, gate on engineer-declared inputs, require fresh approval per round, and wire the bounce on in serve. Part 2, which this depends on, has landed on main (PR #409), so the work can start. The engineer is the only triage role configured, so it plans the whole change.

## Routing
- triage:engineer: implementation steps and test plan across the close-out label effects, job record and Resume, the triage fan-in gate, the approval marker and the serve wiring.
- worker engineer: Go changes with unit tests and the matching proposal updates.

## Open issues
- The issue asks for an architect review of the plan (persisted round state and lead re-arm) before approval, run by the operator. No architect triage role exists in this repository, so a maintainer must do that review by hand before approving.
- The change touches several packages, stored job state and the label state machine. It is large for one worker run, so the triage engineer should check the size and split the work if needed.
