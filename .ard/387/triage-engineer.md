---
role: engineer
status: done
attempt: 1
---
Plans a new combined end-to-end harness test in `internal/servecmd` covering the bounce through reviewers plus the three negative paths, and the remaining `docs/proposal.md` updates (flowchart, label state machine, contract, triage gate, worker step 9, starter worker, configuration). Size L, risk low: tests and docs only, no protected paths.

## Open issues
- The live prompt copies under `.ard/` still need a human pull request.
- Driving triage, worker and reviewer loops in one harness may be fiddly. The worker should drive each loop by explicit ticks.
