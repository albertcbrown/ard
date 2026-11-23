---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds conflict rounds: when the merger computes a conflict with main on a ready PR, it requests a review-fix round with reason conflict instead of going to needs-human, and the worker resolves union conflicts in its worktree. The dependencies are merged and the existing round API covers the mechanism, so this routes to the engineer for planning and implementation. The worker prompt section stays out of the agent's change because that file is protected.

## Routing
- triage:engineer: implementation steps across the merger, the review-fix round flow and the worker conflict input, plus the end-to-end fake test, the overlapping-logic test, the round-cap test and the proposal update.
- worker engineer: Go code, tests and docs/proposal.md changes. It must not edit anything under .ard/.

## Open issues
- The issue also needs a conflict-round section in .ard/workers/engineer/prompt.txt. That path is protected, so a maintainer has to make that change in a separate human PR. The agent's PR should not include it.
- The change touches several packages (merger, reviewfix, worker input, controller code lane). There is no architect role, so the engineer plan should spell out the interfaces between them.
- Related work on re-requesting stale reviews may overlap with the review re-run requirement, so the two should be coordinated.
