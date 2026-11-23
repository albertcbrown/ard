---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds a new deterministic starter scanner for error codes. It follows the existing cve starter, so it needs no controller changes, and the owner has already answered the plan's open questions. Engineer is the only triage role and the only worker that exist here, so the issue goes to engineer triage and then to the engineer worker.

## Routing
- triage:engineer: confirm the implementation steps, the file list and the test plan for the scanner role directory, its tests and the docs updates.
- worker engineer: add the starter role under internal/roles/scanners/, the Go unit and integration tests, and the proposal and CLAUDE.md edits.

## Open issues
- The change edits CLAUDE.md and adds a Dockerfile under internal/roles/. CLAUDE.md is a protected path in the merge configuration, so the pull request will wait for a human approval.
- The plan needs two human steps before merge because worker jobs have no Docker: verifying the base-image digest and running the integration test once.
- The plan expects textual conflicts with other pull requests that add starter roles (the sibling scanner issues and the security reviewer). Those should land one at a time.
- Product, architecture and documentation planning roles do not exist in this repository. The design and documentation parts are covered by the plan already in the issue.
