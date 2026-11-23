---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the first slice of the missing-tests scanner: a standalone Go helper under the embedded starter-role sources, plus its fixtures and table-driven tests. It is not wired into the starter tree, so nothing changes for users and only the engineer needs to plan it before the engineer worker builds it.

## Routing
- triage:engineer: turn the design already settled in #50 into concrete steps for the helper, its fixtures and its tests, keeping the diff within the worker's size cap.
- worker engineer: new Go code with table-driven tests, run by make test.

## Open issues
- The helper sits in internal/roles/scanners/, which is the embedded source for ard init's starter tree. The engineer plan should make sure that adding the directory does not change what ard init writes, or the second done-when item fails.
- The only triage roles here are lead and engineer, so no architect or product-manager pass is possible. The design questions were settled on #50.
