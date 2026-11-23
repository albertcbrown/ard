---
role: engineer
status: done
attempt: 1
---
Makes the triage fan-in gate check that the plan file exists on main before it asks for approval or starts a worker. When it is missing, the gate posts one comment and sets needs-human instead. The plan has six steps with tests for the missing-plan, error and unchanged paths. Size M, risk medium.

## Open issues
- The triage loop has no way to read repository files today, so the plan adds an optional `PlanExists` dependency, filled in from the controller clone with a fetch of main.
- A lead-only triage run never writes a plan file, so it will now stop at needs-human. This follows the requested behaviour, but a maintainer may want to confirm it.
- The proposal's triage section and the Gate 2 runbook both describe the ready-for-approval step, so both get a note.
