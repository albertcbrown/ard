---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds read-only checks to `ard setup` for the main branch ruleset, the App's bypass and permissions, and the repository merge settings. On drift it prints the fix to apply. It also adds a rulesets read method to the GitHub reader interface and its fake. The setup skeleton it depends on (#346) has landed on main, so it can start now and goes to the engineer for planning and implementation.

## Routing
- triage:engineer: plan the new check rows in internal/setupcmd, the built-in policy, the `manual` result when admin read is missing, the rulesets reader method with its fake, and the tests.
- worker engineer: Go change in internal/setupcmd and internal/github with unit tests.

## Open issues
- No architect, product-manager or documentation-planner role exists in this repository. The new `github.Reader` method and the user-visible `ard setup` output therefore get planned only by the engineer role, and docs for setup may need a maintainer's look.
- The checks read admin-scoped settings. A maintainer should confirm the token scopes that `ard setup` expects match the planned `manual` fallback.
