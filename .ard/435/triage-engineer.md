---
role: engineer
status: done
attempt: 1
---
Plans a bounded retry of the pull request creation with the shared transient classification, a new non-transient `pr_create_failed` cause that records the pushed branch and head on the job, and an agent-free re-open when the label is removed and the branch tip is unchanged. Size L, risk medium because it adds a state column, a close-out cause and a new worker finish path.

## Open issues
- The push-retry work has already landed, so the plan reuses the existing transient check and builds no new classifier.
- The job record gets one nullable column through an additive migration, so existing records are unaffected.
- The deployed worker prompt under `.ard/workers/` is a protected path. The plan edits only the built-in template, and a maintainer must sync the deployed copy by hand.
- A step in the plan asks the worker to confirm that close-out can produce `done` without an agent role file. If it cannot, a small synthetic-result path in close-out is needed, which may add to the size.
