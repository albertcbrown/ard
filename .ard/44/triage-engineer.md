---
role: engineer
status: done
attempt: 1
---
Adds a starter license-review reviewer to the embedded roles: a prompt, a permissive default allowlist, a starter config override with a dependency-file path rule, tests and proposal docs. The plan has ten steps. Size M, risk low.

## Open issues
- The CLAUDE.md layout edit touches a protected path, so the PR waits for a human approval at merge.
- The eng-review prompt has no round-2 input, so none was added here. #41 feeds findings to the worker, not the reviewer.
- Adding the role to this repository's own reviewer directory and config, and the scratch-repo smoke test, are human follow-ups.
- #43 edits the same starter lists, so expect trivial merge conflicts.
