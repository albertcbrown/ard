---
role: engineer
status: done
attempt: 1
---
Adds a per-role network setting to the config loader and carries it through the executor Job and Handle, the agent environment, the host warning and the agent prompt, with a new config check warning. Nothing is enforced. The plan has 15 steps with tests for each part. Size M to L, risk low to medium.

## Open issues
- The plan renders the resolved config's network value as a string ("true", "false (advisory)", "false (advisory, host)") so the suffixes can be printed. A maintainer may prefer a separate field.
- The worker's test step is left with network on, so only the agent step is marked advisory.
