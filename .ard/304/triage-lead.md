---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A documentation-only change: a new operator runbook for onboarding another repository, plus a note in the proposal that limits and credentials apply to each serve process separately. It needs only the engineer's plan and the engineer worker; no code, configuration or protected paths are involved.

## Routing
- triage:engineer: lay out the runbook sections, the worked example for limits across several processes, and the proposal edits in Rollout and Configuration.
- worker engineer: write the new runbook and edit the proposal.

## Open issues
- docs/runbooks/ already exists and is listed in docs/layout.md, so the layout update is probably limited to mentioning the new runbook, if it is needed at all.
- The runbook refers to work that has not landed yet (PAT support, the shared roles repo, live cluster jobs). It should present those as conditional rather than describe behaviour that does not exist.
- The runbook describes copying a workflow under .github/ into the target repository. That is a manual step for the operator; this change must not edit .github/ in this repository.
