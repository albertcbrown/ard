---
role: engineer
status: done
attempt: 1
---
Adds the network bounce to the triage engineer in the close-out package: a round helper, the label request and remove phases, the network-request triage input, a one-round loop guard and the starter prompt copies. It stays off because the new dependency is left unset in servecmd. The plan has ten steps with tests for each case in the acceptance list. Size L, risk medium.

## Open issues
- The branch-exists check relies on the job's branch at close-out, which the worker sets on its first push. A new `Pushed` flag on the worker result covers the case the issue names explicitly.
- The loop guard counts every closed worker close-out with the needs-network outcome, including ones from before the bounce existed.
- The live prompts under `.ard/` are protected, so a human PR must follow.
