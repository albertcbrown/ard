---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The controller should mark a worker's draft pull request ready for review once every requested review has passed at the current head, and the proposal and Gate 2 runbook should be updated to match. This is a clear, bounded change to the reviewer loop plus documentation, so it goes to the engineer for a plan and implementation.

## Routing
- triage:engineer: implementation steps for the readiness check in the reviewer loop and per-tick sweep, the effects-layer mark-ready call with dry-run and retry behaviour, the tests listed in the issue, and the doc updates.
- worker engineer: code change with unit tests and doc edits.

## Open issues
- Only the engineer triage role exists, so no architect, product or documentation planner reviews the behaviour change or the proposal edit; a maintainer may want to confirm the proposal wording during review.
- The change touches only code and docs, not protected paths, but it changes when pull requests leave draft, which affects the human merge workflow.
