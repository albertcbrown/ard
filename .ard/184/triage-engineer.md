---
role: engineer
status: done
attempt: 1
---
Makes review close-out end `stale` instead of `failed` when the PR was merged or closed or its branch is gone: it checks PR state before committing and treats a missing branch the same way. It skips the commit, the failure label and the failure comment, logs `review.pr_gone`, and still posts the review best-effort. Tests and docs are included. Size M, risk low to medium.

## Open issues
- The plan makes no worker-path change: a worker's draft PR cannot be merged while the worker runs, and a human deleting its branch mid-job stays a genuine failure.
- Gate 2 reporting needs no code change, since stale jobs complete rather than fail. The plan adds one explanatory sentence to the Gate 2 runbook.
