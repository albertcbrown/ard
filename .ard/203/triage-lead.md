---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a new embedded trufflehog scanner role that sends its findings straight to a human, plus a scanner-level `route` config key and a secret scan over every scanner's findings before they are filed. The owner has already settled the design decisions, and the scanner pipeline it builds on (#23) already exists in the tree, so the issue goes to the engineer for implementation steps and then to the engineer worker.

## Routing
- triage:engineer: break the work into steps across the new role directory, the config schema and `ard config check`, the scanner pipeline's labels and join logic, the pre-filing secret guard, the starter set, the Dockerfiles, tests and docs. Only engineer exists as a triage role, so it also covers the cross-package and config parts an architect or documentation planner would normally take.
- worker engineer: code, tests and docs. It is the only worker role.

## Open issues
- The change edits the root `Dockerfile` and `CLAUDE.md`, which are in `protected_paths`, so the PR will wait for a human approval.
- Enabling the scanner on this repository's own `.ard/` is left to a human PR (protected path) and is not part of the worker's change. It must also wait until the three URI test literals are fixed.
- The scope is large (a new role, a config key, a pipeline change, two Dockerfiles, and unit, integration and Docker tests). A maintainer may want the engineer's plan to split it into more than one PR.
- The one-line note on #53 is a GitHub comment, not a repository change, so a person or the controller has to post it.
