---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This was blocked waiting on #215. The pre-filing guard has now landed in internal/scanner/guard.go, and the pipeline has guard tests, so the work can start. The change makes each scanner post one notice per failure streak to the pinned status issue, and one more when it recovers. It touches the scanner loop and state, the status-issue code in internal/controller and the proposal doc. It is a bounded change for the engineer.

## Routing
- triage:engineer: implementation steps for streak tracking in scanner state that survives a restart, the status-issue post through the effect layer with dry-run, and the table-driven, restart and planted-marker redaction tests.
- worker engineer: code, tests and the proposal's status-issue section.

## Open issues
- The change adds a field to stored scanner state. No architect role exists, so the engineer's plan should cover the state format change and how existing state files load without it.
- The issue says this must land before #217, so #217 should wait for this one.
- eng-review runs on every pull request without a when: rule, so no review entry is needed.
