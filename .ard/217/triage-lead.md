---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds host and Docker integration tests for the trufflehog scanner role and updates the proposal and CLAUDE.md. It changes no controller code. Its dependencies (the trufflehog role, the scanner route key and the scanner failure streaks) are already on main, so the work can start now. It goes to the engineer planner and the engineer worker.

## Routing
- triage:engineer: check that the plan's step 0 still matches what landed on main, then confirm the test helpers, the pinned-version check and the docs checklist.
- worker engineer: two new integration-tagged test files under internal/roles plus documentation edits.

## Open issues
- CLAUDE.md is a protected path, so a human must approve the merge.
- The worker's `make test` does not run the integration-tagged tests, which need Docker, the network and the pinned trufflehog binary. The approving maintainer has to run them by hand and record the results on the pull request.
- The product-manager, architect and documentation-planner triage roles do not exist in this repository, so the engineer plan has to cover the docs changes too.
