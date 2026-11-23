---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A bug fix in the triage fan-in gate: it should refuse to announce readiness for approval when the issue has no plan file and flag the issue for a human instead. The change stays within the triage package, its tests and the proposal's triage section, so the engineer plan is enough.

## Routing
- triage:engineer: implementation steps for the missing-plan check in the fan-in gate, the fake-based test for the no-plan case, a regression check for the existing path, and the proposal update.
- worker engineer: code change in the triage gate with unit tests and the doc edit.

## Open issues
- The fix changes behaviour that a maintainer triggers by removing labels by hand; the runbook for Gate 2 also describes the ready-for-approval step and may need a matching note.
