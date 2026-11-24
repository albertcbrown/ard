---
role: engineer
status: done
attempt: 1
---
Adds a patch-id comparison to the shared head-cover check so a path changed by both the PR and main stays covered when the PR's own patch to it is unchanged after a clean update-branch merge. It wires the rule into the merger's review gate and approval check, logs the covered paths, and updates the proposal. The plan has five steps with real-repo tests for each acceptance case. Size M, risk medium because it loosens a security-relevant gate.

## Open issues
- The old merge base is derived from the first update-branch merge's second parent, since main's tip at review time is not recorded. A maintainer may want to confirm that choice.
- The cover check's git interface widens, so test fakes in the merger and reviewer packages need updating.
