---
role: engineer
status: done
attempt: 1
---
Plans a startup GitHub connectivity check, live-run support for a personal access token, a `gh` CLI token fallback held only in memory, and doc updates in the proposal, README and runbooks. Self-review is decided as follows: reviews are already posted as comments only, so a token user's own approval is simply not counted and protected-path approvals must come from another write-access user. Size L, risk medium because it touches credential handling.

## Open issues
- A maintainer should confirm the self-review decision, since it means a token setup needs a second person to approve protected-path PRs.
- CLAUDE.md still says GitHub access is through a GitHub App installation token. It is a protected path, so a maintainer must update that line by hand.
- The bot-commit audit workflow under .github/ is only documented, not changed, because agents may not edit it.
