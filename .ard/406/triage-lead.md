---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Routed to the engineer for planning and implementation. When a close-out finishes, the controller will wake the loop for the next stage, and a cheap controller watch will spot human approvals and unblocks. The change reuses the per-loop wake that is already merged, adds a new interval key to the configuration schema, and updates the proposal document.

## Routing
- triage:engineer: plan the close-out wake hook, the approval/unblock watch, the new interval setting with its default and minimum, pause and dry-run handling, logging, the fake-clock tests and the proposal updates.
- worker engineer: controller code change in Go with unit tests and doc updates.

## Open issues
- This repository has no architect, product-manager or documentation-planner triage roles. The change touches several controller areas, adds a configuration key (intervals.approvals) and changes documented behaviour, so the engineer's plan has to cover the schema, validation and proposal updates.
- The change edits ard's own code and docs. It does not edit .ard/config.yaml. Rolling back the stopgap interval change is a separate human PR, as the issue says.
