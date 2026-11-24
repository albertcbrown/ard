---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Changes the merger's review-cover check so that a path changed by both the pull request and main still counts as covered when the pull request's own patch to that path is the same after an update-branch merge. The change is in the prgate cover logic and its two callers in the merger, plus tests and a proposal update. Only the engineer triage role exists, so it writes the plan and the engineer worker implements it.

## Routing
- triage:engineer: implementation steps for the patch-id comparison in the prgate package, wiring into the merger's review gate and protected-path approval check, the new log event, gittest-based tests for each acceptance case, and the proposal's merger section.
- worker engineer: Go code change with unit and integration tests and a docs update.

## Open issues
- This loosens a security-relevant gate, and it also applies to the protected-path approval check. The plan should keep the fail-closed behaviour on any git error and check strictly that the head was reached only through update-branch merges. Reviewers should look closely at this.
- No architect or product-manager triage role exists in this repository, so the engineer plan has to cover the design choices too, such as how the old merge base is found at review time.
