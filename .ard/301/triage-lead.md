---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This updates the starter role prompts shipped under internal/roles/ (worker, triage lead, triage engineer, eng-review and security-review) so they cover per-role network access, adds prompt-content tests that pin the new sentences, and aligns the starter-role paragraphs in docs/proposal.md. Both dependencies appear to have landed already (the worker-roles line in the triage message and the fetched-input record), so the work can start now. The live prompts under .ard/ are out of scope and follow in a separate human PR.

## Routing
- triage:engineer: list the per-role prompt edits, the key sentences each test in internal/roles pins, and the matching proposal paragraphs.
- worker engineer: edit the starter prompt text, add the prompt-content tests and update docs/proposal.md.

## Open issues
- The change edits agent instructions under internal/roles/, which merge.protected_paths does not cover, so the PR will not wait for human approval by default. These prompts ship as the starter set for every repository that runs ard init, so a maintainer may want to read them closely before merging.
- The worker must not touch the live role prompts under .ard/triage/, .ard/workers/ or .ard/reviewers/. Those are protected and are meant to follow in a human PR.
- The CAPTURE.md format and the needs-network path are defined in related issues. The engineer should check that the prompt wording matches what actually landed for those issues.
