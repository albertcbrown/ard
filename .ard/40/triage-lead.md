---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds four starter triage prompts (product-manager, architect, designer, documentation-planner) with their starter config, tests and proposal text under internal/roles/, internal/initcmd tests and docs. None of that is a protected path, so it can go to the engineer. Of the planning roles, only the triage engineer exists in this repository, so it is the only one routed.

## Routing
- triage:engineer: turn the existing plan into steps and a test plan for the prompts, starter config, table-driven tests and proposal updates, and decide whether to split it into two PRs.
- worker engineer: edits under internal/roles/, internal/initcmd tests and docs/proposal.md. No controller code changes.

## Open issues
- The issue's own plan says this work should be human-led, or done by an implementer under close human review, not by ard's worker, because the output is security-relevant prompt text. A maintainer should confirm that ard's worker may take it before approving the worker job.
- internal/roles/ is not in merge.protected_paths, so a worker PR that edits starter prompts would not wait for human approval. The owner agreed to protect that path in a human follow-up. It may be worth doing that before this PR merges.
- The issue's done condition needs a live run after a human copies the roles into .ard/triage/ and updates .ard/config.yaml. Agents may not change those protected paths, so that step stays with a person.
- The architect, product-manager, designer and documentation-planner roles do not exist here yet, so they could not be routed, even though the change spans several packages and documented behaviour.
