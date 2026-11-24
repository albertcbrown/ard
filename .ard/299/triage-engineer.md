---
role: engineer
status: done
attempt: 1
---
Adds a fixed "Worker roles" line to the triage user message, filled from the resolved worker roles by the shared agent command builder, so every triage role gets the identical line. The plan has four steps and tests with two worker roles, one with network on and one off. Size S, risk low.

## Open issues
- The line is added in `internal/agentjob/command.go` and `internal/agent/claude/prompt.go`, not in `internal/triage/run.go`, which already calls the builder and needs no edit.
- The triage prompts under `.ard/triage/` may want to refer to the line. Agents may not edit them, so a person has to.
