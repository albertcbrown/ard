---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Slice 3 of the serve command center adds the pause key and the runtime slot override keys to the TUI. It also stops a config reload from wiping the override. The slice it depends on (the read-only TUI) has already merged to main, so work can start now, and the engineer is the only triage role that exists to plan it.

## Routing
- triage:engineer: plan the pause file toggle and unpause wake, the bounded slot override that drains instead of killing jobs, the reload fix in the serve config loader, the dry-run help text and proposal updates, and fake-clock tests that drive config reloads.
- worker engineer: code, tests and docs changes in internal/servecmd, the pool and docs/proposal.md.

## Open issues
- The issue changes how the operator sees behaviour (keys, header, help) and touches the pool and serve reload path. No product, architect or documentation triage roles exist in this repository, so the parent design issue is the reference for those choices.
- The pause from the status-issue label or from config must stay read-only from the UI. Reviewers should check that the pause key never clears those.
