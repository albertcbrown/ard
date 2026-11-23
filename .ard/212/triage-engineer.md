---
role: engineer
status: done
attempt: 1
---
Plans the standalone missing-tests Go helper, its fixtures and its table-driven tests, following the design settled on the parent issue. It also narrows the embed directive in roles.go so the new directory is not written by `ard init`. Size L, risk low.

## Open issues
- The embed directive currently covers the whole scanners directory, so the helper would otherwise change the starter tree. The plan narrows it to the two existing scanners, and part 2 must widen it again.
- The design text came from the parent issue's recorded triage log, since the parent's plan file is not in the repository. A maintainer may want to confirm it is current.
- If the diff nears the 1,000-line cap, two fixtures can move to a follow-up.
