---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Part C adds a new test package that exercises the version workflow's scripts, wires the version into the binary through Makefile ldflags with a build-info fallback, and updates the docs. Part B's workflow has already landed on main, so this can start now. The engineer plans it and the engineer worker implements it.

## Routing
- triage:engineer: steps for the new internal/versioncheck tests and fixtures, the Makefile and `ard version` changes, the doc updates, and the test plan.
- worker engineer: code, tests and docs, with `make lint test` passing.

## Open issues
- The dependency on part B looks met: .github/workflows/version.yml exists on main (commit 0e56ccc, #281).
- The CLAUDE.md layout edit touches a protected path, so the PR will wait for a human approval. A recent commit says the layout check now reads docs/layout.md, so the layout line may belong there instead. The planner should confirm which file.
- If the tests find a bug in version.yml, the worker must report it and not change it. .github/ is off-limits to agents, so a human makes that fix.
- The issue suggests the Makefile and `ard version` changes could ship first as a separate small PR. A maintainer decides whether to split this issue.
