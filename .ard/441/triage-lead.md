---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This fixes a data race in a servecmd unit test that shows up under the race detector. It is a narrow test change, plus a fix to runWithUI only if the race detector also finds a race there, so it only needs engineer planning and the engineer worker.

## Routing
- triage:engineer: plan the test synchronisation (pass the program over a channel, make the stopped flag atomic, remove the sleeps), the check of runWithUI under the race detector, and the verification commands.
- worker engineer: change the test, and runWithUI if needed, then run the race-enabled test commands.

## Open issues
- The issue suggests also running tests with the race detector routinely. A Makefile target is within scope. Changing the CI workflow under .github/ is not open to agents, so a maintainer would have to make that change.
