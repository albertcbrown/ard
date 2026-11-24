---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a refactor with no behaviour change. It moves the filesystem half of the local executor into a new shared workspace package under internal/executor, adds a small Docker environment tweak and updates the layout docs. The prerequisite design record (#312) has already merged, so the work can start. Only the engineer triage role and the engineer worker exist here.

## Routing
- triage:engineer: turn the requested package split into ordered steps and a test plan. The change touches several files in internal/executor and moves code that guards security (scrub, the input symlink guard, rollback order).
- worker engineer: Go code move with the new workspace and env-policy tests and the docs/layout.md update. The existing local executor tests must pass unchanged.

## Open issues
- The issue body asks for a role-directory copy option (CopyRole). The later revised plan in the comments, which follows design revision 3, drops that option. A maintainer should confirm that CopyRole is out of scope so the engineer plans the right version.
- The comments estimate the diff at about 1,400 lines (size L, not M), close to the 2,000-line warning. It stays under only if the behaviour tests and execGit remain in the local package.
- No architect, product-manager or documentation-planner role exists in this repository. The cross-package design checks and the docs/proposal.md note therefore fall to the engineer.
