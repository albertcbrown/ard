---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Tightens the review and approval cover added for update-branch merges. A merge will count as an update merge only if it matches a clean mechanical merge of its parents. The file comparison will also notice whitespace changes and moved hunks, and the proposal's merger text will be corrected. The change is security-relevant but stays within the existing cover logic (the git helpers, the PR gate and the proposal document), so only the engineer planner is needed before the engineer worker.

## Routing
- triage:engineer: implementation steps for the mechanical-merge check and the whitespace-exact comparison. Covers failing closed when git lacks the needed merge-tree support, and the real-repo tests for each acceptance criterion.
- worker engineer: code change in the git helper and PR gate packages, with tests and the proposal update.

## Open issues
- The issue leaves two choices open, and the plan should settle them. First, whether to raise ard's minimum git version or only fail closed when merge-tree write-tree support is missing. Second, whether to replace the patch-id comparison or keep it next to the new merge check.
- The approval check in the merger relies on this cover. A maintainer should review the resulting PR closely, since a mistake could keep a stale human protected-path approval valid.
- Issue 323 is waiting on this one.
