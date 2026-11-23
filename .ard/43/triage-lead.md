---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The revised issue now asks only for a starter security reviewer that ships with ard init. The work is a new prompt under internal/roles/reviewers/, one starter override, test list updates and doc edits. That package is not one agents are barred from, and the earlier dependency and fixture questions are marked as decided, so this goes to the engineer planner and the engineer worker.

## Routing
- triage:engineer: confirm the file list, the prompt's required strings and size cap, and the test updates in internal/roles and internal/initcmd against current main.
- worker engineer: add the starter role prompt, the override, the tests and the documentation edits; no Go behaviour change.

## Open issues
- The change edits CLAUDE.md, which is in merge.protected_paths, so the pull request will need a human approval before merge.
- Adding the role to this repository's own .ard/reviewers/ and .ard/config.yaml is left to a separate human pull request. Agents may not make that change.
- Acceptance includes a manual paid-model smoke run on a scratch repository, which the owner has to do. The worker cannot complete that checkbox.
- The issue body includes a suggested routing. This decision was reached separately from the scope described and does not rely on that suggestion.
- Rebase over issue 41 or issue 42 if either merges first. The overlap is limited to test lists and the eng-review prompt.
- Issue 205 now tracks the deterministic tool step (part B). It is out of scope here.
