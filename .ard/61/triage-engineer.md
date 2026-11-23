---
role: engineer
status: done
attempt: 1
---
Adds a new janitor loop in its own package that finds closed issues past the 90-day cutoff, re-checks each one, and deletes its `.ard/<issue#>/` files through the shared main committer. It also adds a git tree-listing helper, wires the loop into serve, and updates the docs. Size M, risk medium because it deletes files on main, though the committer's path guard confines it to numbered issue directories.

## Open issues
- The committer has no way to list a directory, so the plan adds a listing helper. The worker must confirm which fetched main ref the controller clone exposes before wiring it.
- The monthly cadence is kept in memory, so a restart causes one extra, harmless pass.
- The `CLAUDE.md` layout update is a protected path, so the PR will wait for human approval.
- The plan skips issues carrying the keep or hold labels, which the issue text does not ask for. A maintainer may want to confirm that.
