---
role: engineer
status: done
attempt: 1
---
Adds an embedded starter product-manager-review prompt under internal/roles/, its starter config override gated on product-manager triage, a new test file, updates to the existing starter-list tests and proposal edits. Size M, risk low to medium because the prompt steers a future agent and the starter config changes.

## Open issues
- The issue's CLAUDE.md edit was dropped: CLAUDE.md has no internal/roles/ layout lines, and it is a protected path.
- The existing reviewer-list assertion in the security-review test must be updated to include the new role.
- The shared test-helper extraction is marked optional in the plan, to keep the diff small.
- Done-when is a manual evaluation on a scratch repo; a human should read the prompt itself.
