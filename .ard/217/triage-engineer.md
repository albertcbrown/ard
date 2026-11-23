---
role: engineer
status: done
attempt: 1
---
Adds host and Docker integration tests for the trufflehog scanner role, both failing when the binary is not the pinned version, and fills the gaps in the proposal's trufflehog paragraph. Size M, risk low, since only tests and docs change.

## Open issues
- CLAUDE.md no longer has the Layout line the issue describes: Layout now points to docs/layout.md, which already lists the trufflehog role. The plan therefore leaves CLAUDE.md unchanged, so the checklist item for it is already met and no protected-path approval is needed.
- The integration tests need Docker, the network and the pinned binary. A maintainer must run them by hand and record the results on the pull request, because `make test` does not run them.
