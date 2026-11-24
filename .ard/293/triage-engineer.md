---
role: engineer
status: done
attempt: 1
---
Adds a repo-context section to each of the 11 embedded starter prompts under internal/roles, covering the files each role reads, the absent rule, precedence, conflict handling and the reviewer PR-head rule. It also adds a table-driven test and updates docs/proposal.md. Size M, risk low.

## Open issues
- The new files live at `.ard/*.md`, outside the `.ard/N/` directory that the prompts' Trust text calls untrusted, so the worker must reword that text carefully.
- The live prompts under `.ard/triage/`, `.ard/workers/` and `.ard/reviewers/` are not changed. A maintainer syncs them in a separate PR.
