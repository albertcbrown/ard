---
role: engineer
status: done
attempt: 1
---
Adds an approve key to the serve terminal UI: a new effects method runs the operator's own gh (recorded in the ledger under dry-run), a confirm screen defaults to No, a missing gh falls back to the issue URL, and success wakes triage. The plan has nine steps with tests. Size L, risk medium because it spawns a subprocess with the operator's credentials and changes the Effects interface.

## Open issues
- The snapshot currently holds no approval-queue data (title, worker role, plan link). The plan adds an in-memory queue fed by the triage gate; if the parent design or #414 already defines one, the worker should reuse it.
- The parent design issue was not available to the planner, so details such as key help text and status lines follow existing UI conventions.
