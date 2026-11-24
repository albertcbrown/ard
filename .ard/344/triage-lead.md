---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This replaces the conflict and force handling in the init planner with an upgrade planner that reads a lock file of starter hashes, and it adds that lock file to the init path checks and to the starter's protected paths. Both prerequisites are already on main: the design docs change for issue 343 (merged as PR 352) and repo context docs part A (PR 329). So the work can start now and goes to the engineer.

## Routing
- triage:engineer: implementation steps for the pure plan function in internal/initcmd, reading and writing the lock, the per-file cases, and the table tests, including one for a hand-edited lock.
- worker engineer: Go change in internal/initcmd plus the starter config template, with unit tests.

## Open issues
- The change is user-visible (new init behaviour and a new file under .ard/), but there is no product-manager, architect or documentation-planner role in this repository, so the engineer's plan has to cover the docs updates too.
- The starter's merge.protected_paths gains an entry. That edits the template that ships in the binary, not this repository's .ard/config.yaml. If maintainers want the lock protected here as well, they must add it to .ard/config.yaml by hand.
