---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is part A of the limits-aware scheduling work: it records subscription usage-window readings from the agent stream and logs them, without gating any job. Its dependency, the Reaper fix from PR 235, is already on main, so work can start. Only the engineer triage role exists here, so it plans the steps and the engineer worker implements them.

## Routing
- triage:engineer: implementation steps across internal/agent, the claude parser, internal/limits and internal/servecmd, plus the test plan (OAuth step recorded, API-key step not, Observe error still returns the result, the ErrNoResult path still records windows).
- worker engineer: code, unit tests and the matching docs/proposal.md paragraph.

## Open issues
- The change spans several packages, adds a new type to a stored and logged reading, and edits docs/proposal.md. No architect, product-manager or documentation-planner role exists, so the design rests on the implementation plan in the parent issue.
- Two done-when items need a person: ticking the spike box on the parent issue, and watching a few days of real runs to confirm readings are logged and no job is gated. A worker cannot do either.
