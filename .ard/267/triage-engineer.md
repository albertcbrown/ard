---
role: engineer
status: done
attempt: 1
---
Adds a new prwatch loop that observes ard's pull requests every five minutes and records human merges. It also notifies on closed-unmerged PRs, long-pending checks and red drafts. Check evaluation moves from the merger into the shared prgate package, with new config keys, a state table and docs. The plan has seven steps. Size L, risk medium.

## Open issues
- The change edits CLAUDE.md, a protected path, so the pull request needs a human approval.
- The migration number (0008) and the way the status issue is located should be confirmed against the repository when implementing.
- If the diff passes the warning limit, the worker may split it as the issue suggests: shared checks, config and table first, then the four actions.
