---
role: engineer
status: done
attempt: 1
---
Adds the missing-tests scan script and pinned Dockerfile, embeds them as a starter role with a config entry, updates the starter lists in the tests, and adds end-to-end and Docker integration tests plus docs. The plan has eight steps. Size L, risk low to medium, because the scan runs the target repository's own tests.

## Open issues
- The layout list now lives in the layout doc, so the plan edits that file instead of the agent-instructions file, which is a protected path.
- The new Dockerfile sits under internal/roles/, so it should not match the protected root Dockerfile entry; a maintainer may want to confirm.
- The group size of the starter entry follows the proposal table (10 per package); the worker should check it against the helper's group handling.
- Enabling the scanner in this repository's own .ard/ stays a separate human change.
