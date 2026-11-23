---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a monthly janitor that removes per-issue directories under .ard/ for issues closed more than 90 days ago, committing the deletion through the existing committer. The dependency the issue names appears to have landed already (the committer package exists and cites it), so the work can start now.

## Routing
- triage:engineer: implementation steps for the janitor loop, how it finds closed issues and their close dates, how it commits through the committer, and a test plan.
- worker engineer: code change with unit tests.

## Open issues
- The repository has no architect, product-manager or documentation-planner triage roles. This change adds a new background loop and may touch serve's loop list and the proposal docs, so a maintainer may want to check the design during review.
- The janitor deletes files from main. The engineer's plan should make sure it never touches the protected role and config directories under .ard/, or the per-issue directory of an issue that is still open or was reopened.
