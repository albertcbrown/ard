---
role: lead
status: blocked
attempt: 1
---
The request is a clear change: post one notice per scanner failure streak, and one on recovery, to the pinned status issue. One of the outcome classes it must cover, and the reason it must name, is the pre-filing secret guard from #215. That guard is not in the code yet (#215 is still open in docs/TODO.md, and internal/scanner/pipeline.go has no guard), so the work should wait until #215 lands.

## Routing
None.

When unblocked, the expected routing is:
- triage:engineer: implementation steps for streak tracking in scanner state, the status-issue post through the effect layer, and the table-driven, restart and redaction tests.
- worker engineer: code, tests and the proposal's status-issue section.
- No other triage roles exist in this repository yet. eng-review runs on every pull request without a when: rule, so no review entry is needed.

## Open issues
- Waits for #215 (pre-filing guard), which supplies the guard outcome and rule name this notice reports.
- The issue says it must land before #217, so #217 is in turn behind this one.
- The change touches scanner state, which is stored data. No architect role exists, so the engineer's plan should cover the state schema change and its migration.
- A maintainer could instead choose to start now with the scan_failed and max_open classes, and add the guard class when #215 lands.
