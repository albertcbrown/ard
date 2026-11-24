---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The budgets work this issue waited on has landed: both parts of issue 57 are on main (commits 319be49 and 48af6fe), so spend per role and repository is now available. The daily digest on the status issue can start. It covers queue depth by label, jobs run, failures and spend, all read from the events table. The engineer plans it and the engineer worker builds it.

## Routing
- triage:engineer: plan a daily digest loop in the serve controller, next to the existing status-issue and budget notice code in internal/servecmd. It reads queue depth, job counts, failures and spend from the events table. The plan also needs a test plan.
- worker engineer: the code change with unit tests.

## Open issues
- The digest adds a recurring post to the status issue, which users will see. This repository has no product-manager or documentation-planner role, so the engineer's plan should say whether docs/proposal.md or the README needs an update.
- docs/TODO.md still lists the budgets work as open. A maintainer reported that this is stale, and the git history confirms it.
