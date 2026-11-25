---
role: engineer
status: done
attempt: 1
---
Adds a `needs-network` worker status with a strictly validated `network:` list in the role-file parser, re-parses it after redaction, and adds an `OutcomeNeedsNetwork` close-out outcome that ends as needs-human with the list in the comment. The plan has ten steps with tests for the parser, `Build` (including the Reaper path), `prepare` and the comment. Size M, risk low to medium.

## Open issues
- The parent design issue was not available as a file, so the plan follows the scope in this issue only.
- The new outcome also needs one-line additions in the cost table and the Reaper's two outcome switches; these are in the plan.
