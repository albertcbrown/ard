---
role: lead
status: blocked
attempt: 1
---
This issue adds a starter product-manager-review reviewer under internal/roles/. It is blocked until the product-manager triage role from #40 lands. Its starter override gates the reviewer on that triage role, and the config loader rejects a when.triage name with no matching role directory. Without that role, the embedded starter config and ard init would break.

## Routing
None while blocked. Once #40's product-manager role has merged, route to triage:engineer (the plan already has detailed engineer steps and the owner has settled the open questions) and to worker engineer. The deliverable is in internal/roles/, docs/proposal.md and CLAUDE.md, not in the protected .ard/ role directories.

## Open issues
- Waits for #40, but only the product-manager triage role. internal/roles/triage/ holds only engineer and lead today.
- #42 looks merged: config.When.Matches exists, and license-review already uses a when: rule. Confirm that it uses OR semantics for triage and paths before this proceeds.
- The plan edits CLAUDE.md, which is a protected merge path, so the PR will wait for a human approval.
- Turning the role on in ard's own .ard/ is a separate human follow-up PR, as the plan says.
