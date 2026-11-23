---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a Go-only missing-tests scanner to the starter roles embedded for ard init. It follows the pattern of the existing cve scanner: a role directory with a scan driver, a helper and a Dockerfile, plus a starter config entry, tests and docs. The owner has already answered the plan's open questions and the scanner pipeline it depends on is in place, so it goes to the engineer for implementation steps.

## Routing
- triage:engineer: turn the agreed plan into concrete steps and a test plan for the new role directory under internal/roles, its fixtures, the starter config and the init tests.
- worker engineer: self-contained Go and shell change following the cve scanner pattern.

## Open issues
- The change also edits CLAUDE.md (the Layout line), which is a protected path, so merging needs a human approval.
- Turning the scanner on for ard itself (under .ard/scanners/, .ard/config.yaml and deploy/) is a separate human follow-up. The worker must not touch those paths.
- The issue says it depends on an earlier scanner pipeline issue and states that this work is finished. A maintainer may want to confirm that before the worker starts.
