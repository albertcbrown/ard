---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the first slice of the larger trufflehog scanner work. It adds a per-scanner routing key to the configuration schema and the scanner pipeline, and a fail-closed secret check that runs before any scanner files or edits an issue. The change is clear and scoped, so it goes to the engineer for planning and implementation.

## Routing
- triage:engineer: implementation steps and test plan covering the config schema and validation, label handling in the scanner pipeline's create, join and stale-close paths, the pre-filing secret guard and its fail-closed behaviour, and the proposal doc update.
- worker engineer: Go code, tests and docs change.

## Open issues
- Only the engineer triage role exists here. The change adds a configuration key and touches the shared scanner pipeline and documented behaviour, so it would normally also get architect and documentation-planner review. The engineer plan should cover those concerns.
- The guard optionally depends on gitleaks redaction from another issue. The plan should confirm that the guard behaves correctly whether or not that setting is present.
- The issue sizes this as one part of a three-way split under a 1,000-line worker cap. If the plan exceeds that cap, a maintainer may need to split it further.
