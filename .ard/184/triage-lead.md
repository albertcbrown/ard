---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Review close-out should treat a pull request that was merged or closed, or whose branch is gone, as stale instead of failed, so no failure label or comment appears and Gate 2 does not count it as an executor failure. The change is mainly in the reviewer close-out path (internal/reviewer) and its use of committer errors, with a possible matching check for workers. It also needs tests and a docs update. Only the engineer triage role exists, so it plans the steps alone.

## Routing
- triage:engineer: plan the stale-outcome handling in reviewer close-out, the optional worker-path equivalent, the Gate 2 reporting exclusion, the tests for merged and closed-unmerged PRs, and the docs/proposal.md update.
- worker engineer: code change with unit tests and the docs edit.

## Open issues
- The issue leaves open whether worker jobs can hit the same path. The engineer's plan should settle that.
- No architect or documentation-planner role exists. The change to docs/proposal.md is a small description of internal behaviour, so the engineer covers it.
