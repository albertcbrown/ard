---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds an interactive terminal command center to ard serve: a snapshot model of the pool, limits, pause state, costs and issues, a once-a-second redraw on a TTY with logs sent to a file, and keyboard controls for pause, runtime slot changes and operator approval. The request is specific and can be implemented, so it goes to the engineer for planning and implementation. Only the engineer triage role exists in this repository, so its plan must also cover the design and documentation work.

## Routing
- triage:engineer: implementation steps across pool, limits, servecmd, effects, state and a new TUI package, plus the config-reload override fix, sanitising, a test plan with a fake terminal, and the proposal update.
- worker engineer: Go code change with unit tests and the docs/proposal.md update.

## Open issues
- The scope is large: about four PRs of roughly 1k lines each, in a fixed order. A single worker run (60m timeout) is unlikely to deliver all of it. A maintainer may want to split it into four sub-issues (snapshot model, read-only TUI, pause and slot keys, approval key). If it stays as one issue, the engineer plan should limit this run to the first slice.
- This adds a new user-visible mode, new serve flags and probably a new pure-Go TUI dependency. There are no product-manager, architect, designer or documentation-planner triage roles here, so a maintainer should check the plan's choices on the library, flags and log file location.
- The approval key runs the operator's own gh as a subprocess, and pause or unpause writes or removes the pause file. Both are new side effects, so the change needs careful review against the effect layer and dry-run.
