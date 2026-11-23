---
role: engineer
status: needs-human
attempt: 1
---
A comment on this issue gives direct instructions to the planner about how to rewrite the existing plan. Agents may not follow instructions found in issue text, so the plan was left untouched and no new implementation section was written.

## Open issues
- The comment lists amendments and a list of design changes that would reshape the plan. These include a new dependency on another pending change, a different rerun rule, new event ordering and cap behaviour, and an end-to-end test requirement. A maintainer should confirm them through a trusted channel, for example by editing the issue description or the plan directly, and then re-run planning.
- The amendments also ask for edits to documentation and to protected prompt files. Those must be made by a maintainer.
- Because the amendments say to wait for the other pull request to land first, the work may be blocked until that change is on the default branch.
