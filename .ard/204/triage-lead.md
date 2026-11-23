---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This issue adds the engine that runs prompt.txt scanners as agent jobs, split into four ordered PRs. The prerequisite it named, issue 215, has landed on main: the `route` key is in `internal/config/load.go` and `internal/scanner/guard.go` exists. So the work can start. It goes to the engineer planner and the engineer worker, and the worker's part should stay limited to the first PR (the pure filing rules).

## Routing
- triage:engineer: check the existing plan against main now that the route key and the guard have landed (some PR 1 config checks may already exist), and write the steps for the first PR, which covers the filing rules, the near-duplicate check, the fingerprint grammar, the spend floor and the stale-closer change.
- worker engineer: the first PR is pure, table-tested code inside `internal/config` and `internal/scanner`, which fits the worker.

## Open issues
- The issue's plan says PRs 2 to 4 (executor output mount, agent adapter argv and message, credential path, serve wiring) should be led by a human because they touch invariants 2, 6 and 7 and need integration tests that `make test` does not run. A maintainer should keep the worker on the first PR only and handle the rest by hand.
- The PR 4 proposal update also changes CLAUDE.md. That is a protected path, so it needs a human approval.
- A later owner comment adds a requirement: the stale closer must also expire issues that carry only a scanner label. The engineer plan should say which PR includes it.
- No architect, product-manager or documentation-planner role exists in this repository, though the change spans several packages and changes documented behaviour. The issue's existing plan already covers that design and the proposal updates.
