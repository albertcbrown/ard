---
role: engineer
status: done
attempt: 1
---
Adds a scanner `route` key (config schema, validation, and label handling in the pipeline's create, join and dry-run paths) and a fail-closed pre-filing secret guard that runs on every scanner, then updates the proposal. The plan has eight steps with tests for each field, the routes, the stale closer and the cve fixtures. Size L, risk medium because the shared pipeline serves every scanner.

## Open issues
- Reconcile and the dry-run ledger adoption run before the scan, so they can still refresh local finding rows before the guard runs. The plan puts the guard before any diff, edit, join, create, tick or close, which is the earliest point the findings exist; a maintainer should confirm that is what "no finding row changes" means.
- The stale closer already ignores issues without `ard:pending` and issues with `ard:needs-human`, so no code change is planned there, only tests.
- Running gitleaks once per field would be slow; the worker is told to prefer one buffer per finding.
