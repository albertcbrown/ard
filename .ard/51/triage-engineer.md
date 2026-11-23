---
role: engineer
status: done
attempt: 1
---
Plans PR A only: a new deterministic `docs` starter scanner (a `scan` script plus a stdlib-only Go program) with golden tests, a config override, roles and initcmd test updates, and proposal and CLAUDE.md edits. Size L, risk medium-low because it is additive but autocloses, so it must fail closed.

## Open issues
- The LLM doc-drift scanner is blocked on the LLM scanner engine issue and is not part of this plan.
- The change edits CLAUDE.md, a protected path, so merging needs human approval.
- A human must do the follow-ups agents cannot: add the role to ard's own `.ard/scanners/docs/`, add the override to `.ard/config.yaml`, extend the `deploy/test-repo.sh` starter check, and run the label sync.
