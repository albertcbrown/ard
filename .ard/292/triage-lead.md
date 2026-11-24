---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a well-scoped code, test and doc change. It adds three optional repo context documents to the `.ard/` contract, the starter config's protected paths, the embedded role templates and `ard init`'s create-if-absent plumbing. Only the engineer triage role and the engineer worker exist, so the engineer plans it and implements it.

## Routing
- triage:engineer: implementation steps across internal/roles, internal/committer, internal/initcmd and docs/proposal.md, plus the test plan for each completion criterion.
- worker engineer: Go code, embedded templates, tests and the proposal doc update.

## Open issues
- The change edits the starter template in internal/roles/config.yaml, not this repository's own .ard/config.yaml. Agents may not change that file. If a maintainer wants the three new files protected in this repository too, they have to add them to merge.protected_paths by hand.
- The design is in the parent issue. The planner should check the template headings and the precedence and conflict rules against it rather than inventing them.
