---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the second part of the network inputs back-route. It adds the bounce to the triage engineer in the close-out package, the network-request triage input, the loop guard and the starter prompt copies, and leaves the bounce switched off in servecmd. Part 1 has landed on main, so the work can start now. Only the engineer triage role and the engineer worker exist, so both go to the engineer.

## Routing
- triage:engineer: implementation steps across closeout (round helper, label phases, loop guard in Prepare), the triage input file and the starter prompts under internal/roles/, plus the test plan from the acceptance criteria.
- worker engineer: Go code change with unit tests, and starter prompt copies outside the protected .ard/ tree.

## Open issues
- The change touches the label lifecycle and the proposal, so it would normally go to an architect, but this repository has no architect role. The triage engineer should keep to the design in the parent issue.
- The live prompts under .ard/ are protected and are out of scope here. A human follow-up PR updates them, as the issue says.
- The bounce must stay unwired in servecmd until part 3 lands. Reviewers should check that live behaviour is unchanged.
