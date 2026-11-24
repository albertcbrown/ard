---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This issue adds an optional `fetched:` list to worker role files. Close-out parses it strictly, and the worker PR body renders it as a Fetched inputs list. The issue also brings docs/proposal.md's egress and network text in line with the code. The dependency (part 1, the per-role network setting) has already landed, so the work can start, and engineer is the only triage planner and worker in this repository.

## Routing
- triage:engineer: steps for parsing and validating `fetched:` in internal/closeout and carrying it on the worker result, rendering the PR body in internal/worker/pr.go, and editing the proposal. Includes table tests for each invalid field and PR-body tests with and without entries.
- worker engineer: Go change with unit tests plus the docs/proposal.md edits.

## Open issues
- The change adds to the `.ard/` file contract and touches several places (closeout, worker, docs). No architect or documentation-planner role exists, so the triage engineer should cover the contract wording in the proposal as well.
- The proposal must describe egress "as the code does". The engineer should check which enforcement parts of the tracking issue have landed, so the docs describe what exists today and not what is planned.
- The issue mentions a per-fixture CAPTURE.md convention. The plan should say whether this change adds or updates any CAPTURE.md file, or only documents the convention.
