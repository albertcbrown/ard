---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The committer should treat a GH006 protected-branch rejection on a main-lane push as a lost race when main moved since the lease, retry it within the existing rejection budget, and otherwise fail at once with a distinct protection cause. The change stays in the push error classification and the committer's retry loop, plus the proposal text, so only the engineer plans it.

## Routing
- triage:engineer: steps for the new error class, the re-fetch check of main against the lease, the retry and failure paths, logging, the fake-remote and table tests, and the proposal update.
- worker engineer: code change in the git push helper and the committer, with unit tests and the docs/proposal.md edit.

## Open issues
- Retrying a protection rejection changes what that failure means; a maintainer may want to confirm the rule that a retry happens only when main moved since the lease.
- Only triage roles lead and engineer exist, so no architect or documentation planner reviews the new failure cause or the proposal wording.
