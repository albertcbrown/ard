---
role: engineer
status: done
attempt: 1
---
Adds a per-scanner failure streak, kept in the scanner database's existing key/value table, and posts one notice per streak and one on recovery to the pinned status issue through the effect layer. The notice carries the scanner name and a sanitised reason class only. The plan has six steps and tests for each outcome class, a restart, a planted marker and dry-run. Size M, risk low to medium.

## Open issues
- A timeout run is counted as a `scan_failed` streak, since the issue does not name it. Runs dropped at the global cap, and `error` runs, neither start nor end a streak. A maintainer can change either choice.
- If the reason class changes during one streak, no second notice is posted.
- No schema migration is needed: old state files simply have no streak key.
