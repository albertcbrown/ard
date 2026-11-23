---
role: engineer
status: done
attempt: 1
---
Plans conflict rounds: the merger requests a conflict round through the existing round API instead of going to needs-human, and the worker resolves the merge of main in its worktree, pushed as a two-parent commit through the code lane. Ten steps with tests, including an end-to-end fake test, the overlap case and the cap. Size L, risk high because it changes the committer code lane and the review round state machine.

## Open issues
- The conflict-round section of the worker prompt is under a protected path, so a maintainer must add it in a separate human PR. Until then the worker relies on the conflict input file alone.
- The code lane today writes single-parent commits, so the plan adds an optional merge parent to its request. A maintainer may want to review that design, since it touches security-sensitive code.
- The estimate is near the worker's size limit. The plan names a split point if it goes over.
- Coordinate with the issue on re-requesting stale reviews, which touches the same review re-run path.
