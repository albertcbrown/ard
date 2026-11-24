---
role: engineer
status: done
attempt: 1
---
Adds a no-commit merge helper to gitx, a merge-parent option with guards to the committer code lane, a conflict input and merge-relative diff guard to the worker, and wires the merger to the review fix loop in servecmd. Size L, risk high because it changes the committer's path guard, staged-change check and lease push.

## Open issues
- A maintainer should review the committer changes closely.
- The worker prompt section for conflict rounds is a separate human PR and should merge with or before this one.
- No conflict-round switch exists in the starter config today. The plan wires only `Deps.Rounds` in servecmd, and any change to protected `.ard/` starter files is left to a human.
