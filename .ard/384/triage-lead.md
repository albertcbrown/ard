---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the first part of the network-inputs back-route. It adds a needs-network worker status with a validated network list in the closeout role-file parser. It also re-parses that list after redaction and adds a new close-out outcome that ends as needs-human and posts a comment listing the entries. The scope is concrete and confined to internal/closeout plus the proposal text, so the engineer can plan it and the engineer worker can implement it.

## Routing
- triage:engineer: implementation steps across rolefile.go, prepare.go, plan.go and the comment rendering, plus the rolefile table tests and the Build tests, including the Reaper path.
- worker engineer: Go code change with unit tests and the matching proposal update.

## Open issues
- The issue says the dependency it builds on has landed, but the parent design lives in a separate issue. The planner should read that design for the full contract.
- The change also updates the documented .ard/ contract in docs/proposal.md. Only triage roles for engineering exist here, so no separate documentation or architecture triage is available.
