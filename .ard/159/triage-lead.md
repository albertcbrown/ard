---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The worker picks the PR and commit prefix from the issue's type:* label, and falls back to chore when there is no such label. That fallback is why an unlabelled bug fix got a chore: title. The fix is a small Go change in the worker package plus a note in the proposal's worker section, so only the engineer planner is needed before the engineer worker implements it.

## Routing
- triage:engineer: choose between deriving the prefix differently and dropping it in favour of the issue title with its number, then write the steps for the worker package (CommitType and where the worker builds the commit and PR request), its unit tests and the proposal text.
- worker engineer: Go code change with unit tests and the matching docs update.

## Open issues
- One option in the issue takes the prefix from the lead's routing. That would mean adding a frontmatter key to the lead's output, which requires editing the lead prompt under .ard/triage/. Agents may not change that path, so a maintainer would have to make that part of the change. Options that touch only Go code and docs, such as dropping the prefix or keeping the label mapping with a better fallback, avoid this.
- The current mapping relies on maintainers applying type:* labels at intake. The planner should state whether that stays the intended source.
