---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the second half of conflict rounds. It adds a no-commit merge helper in gitx, a merge-parent option with guards in the committer code lane, conflict input and a merge-relative diff guard in the worker, and it turns on part A's switch in the starter config and servecmd. Part A (#322) has already landed on main, so the work can start now. The engineer is the only triage role that exists, so it gets the plan and the engineer worker does the work.

## Routing
- triage:engineer: implementation steps and a test plan across gitx, committer, worker, servecmd, the starter config and the proposal doc, using the earlier umbrella plan for #255 as background.
- worker engineer: Go code change with unit and end-to-end tests that use the fakes.

## Open issues
- This change touches security: it alters the committer's path guard, staged-change check and lease-push handling for two-parent commits. A maintainer should review the PR closely, beyond what eng-review covers.
- The conflict-round section of the worker prompt is out of scope here and needs its own human PR, which must merge with or before this one. Otherwise the switch turns on while the worker has no prompt guidance for conflict rounds.
- The repository has no architect triage role. The cross-package design rests on the existing umbrella plan and the engineer's section.
