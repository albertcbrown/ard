---
role: engineer
status: done
attempt: 1
---
Checked the existing implementation plan against the current code and kept it: it adds a starter security-review reviewer prompt, one config override, a new test file, test list updates and doc edits, with no Go behaviour change. I corrected the role-list wording in the doc comment and proposal steps, because main now also ships the docs and error-codes scanners. Size M, risk low.

## Open issues
- The CLAUDE.md layout edit is in a protected path, so the pull request needs a human approval.
- The paid-model smoke run on a scratch repo is for the owner, not the worker.
- If a change to the eng-review prompt for review fix rounds merges first, mirror it in the new prompt and its shape test.
