---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a docs-only change. It rewrites the README, the site's getting-started page and the onboarding runbook (docs/runbooks/onboard-repo.md) around the new init-then-setup onboarding flow. Both dependencies are already on main: part C changed `ard init` to write the local tree, and part D added the `ard setup` command (internal/setupcmd). The work can start now and needs only an engineer plan.

## Routing
- triage:engineer: list the files to rewrite and check every documented command and flag against the current `ard init`, `ard setup` and `ard serve --dry-run` code, plus whatever docs checks `make lint test` runs.
- worker engineer: documentation rewrite across README.md, docs/site and docs/runbooks.

## Open issues
- No product-manager or documentation-planner role exists. The flow's order comes from the owner decisions on the parent issue, so the engineer should take the order from there and not redesign it.
- Part E (setup checks for rulesets and bypass) has also landed. Any manual rows that setup reports should match what the current code prints.
