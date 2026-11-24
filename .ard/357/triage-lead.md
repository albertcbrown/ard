---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The committer should retry transient push failures, such as server errors, network failures and token mint errors, with backoff instead of failing the job. If the error is still there when the retry budget runs out, the job should end with a transient close-out cause. The change is internal to the git and committer packages and the job close-out path, so the engineer can plan it directly.

## Routing
- triage:engineer: steps for classifying transient errors in gitx, the backoff retry in the committer lanes (main, code and init), mapping the close-out cause, and the acceptance tests.
- worker engineer: Go code change with table tests against the git fake.

## Open issues
- A comment adds a second case: a protected-branch rejection that looks like a lease race. The plan should decide whether it belongs in this change. If it does, a persistent rejection after a fresh fetch should end the job needing a human, not failed.
- A retry after a push that may have landed must compare upstream with the pushed commit so that it never writes a second commit.
- The close-out change touches how the Reaper sees the job, so the plan should confirm which existing transient cause to reuse.
