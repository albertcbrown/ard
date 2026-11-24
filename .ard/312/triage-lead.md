---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a docs-only change: it records the approved Kubernetes executor design as a decision and updates the matching sections of docs/proposal.md. No code changes. The scope is clear and the owner has answered the open questions in the comments, so the engineer plans the edits and the engineer worker makes them.

## Routing
- triage:engineer: turn the comment plan and the owner's decisions into exact edit steps for docs/proposal.md, and match them to the file's current line numbers.
- worker engineer: edit the documentation (decision record, Executors table, cleanup text, lock mentions, Guardrails).

## Open issues
- The design comment on the parent issue holds decisions 1 to 12, and a gaps file is also referenced. Neither is in the repository, and the worker cannot fetch them. If the engineer cannot rebuild every decision from the issue comments, a maintainer should paste the decision text into the issue or the repository.
- The comment plan describes the upload step as a native sidecar, but the owner's later decision chose a plain second container. The plan should follow the owner's decision.
- The line numbers cited for docs/proposal.md have drifted. For example, the Job TTL text is now near line 317 and the second Lease mention near line 544.
- No architect, product-manager or documentation-planner triage roles exist, so only the engineer plans this. The owner already approved the design.
