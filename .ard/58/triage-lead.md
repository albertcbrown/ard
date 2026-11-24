---
role: lead
status: blocked
attempt: 1
---
The daily digest is a clear change, but spend is one of its required figures. Spend per role and repository comes from the budgets work in issue 57, and docs/TODO.md still lists that issue as open, so this one stays blocked until it lands. Issue 15, the other dependency, is no longer in the open list.

## Routing
None until the dependency lands. After that, the expected routing is triage:engineer with worker engineer. The digest adds a recurring post to the status issue, which users will see, but the product-manager and documentation-planner roles do not exist in this repository.

## Open issues
- Blocked on issue 57 (daily dollar caps per role and repo), which supplies the spend data the digest reports.
- A maintainer could split the work: ship the digest without spend now and add spend after issue 57 lands. If they do, this issue can be re-triaged as done.
