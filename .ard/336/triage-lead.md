---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The cve scanner reported a medium-severity advisory against the idna package resolved for the documentation site build. idna is not pinned directly in docs/site/requirements.txt (it arrives transitively through the MkDocs toolchain), so the narrow fix is a lower-bound constraint at the fixed release, following the existing certifi entry; only the engineer plan and worker are needed.

## Routing
- triage:engineer: confirm the fixed idna version, add the constraint to the docs requirements, and say how to verify the docs build still resolves.
- worker engineer: one-line dependency constraint in docs/site/requirements.txt.

## Open issues
- The affected file only feeds the documentation site build, not the Go binary, so runtime exposure is limited to docs tooling.
