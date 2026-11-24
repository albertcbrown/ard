---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is part 55b of the default agent image work. It adds a host setting that names a default agent image, and the local executor uses that image to run untrusted-input roles that have no Dockerfile under Docker. Its dependency, 55a, has already landed on main, so the work can start now. Only the engineer triage role exists, so it plans the steps and the engineer worker implements them.

## Routing
- triage:engineer: implementation steps for the image-ref validation, the local executor's Prepare and pull path, the read-only clone mount, the host-mode warning change, unit tests and the proposal update.
- worker engineer: Go change in the executor, serve and config-check paths, with unit tests and docs.

## Open issues
- The change controls whether agents that read untrusted input run inside a container or on the host. This makes it security-sensitive. A maintainer should review the no-fallback-to-host behaviour closely.
- The Docker integration test and the live check on the Mac need a person. The worker cannot do them.
- The scope adds a new host setting and changes documented behaviour. No architect, product-manager or documentation-planner role exists in this repository, so the engineer plan has to cover those points itself.
