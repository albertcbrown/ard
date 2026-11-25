---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
When a worker's branch is pushed but opening the pull request fails, the PR should be retried, and later re-opened, from the recorded close-out without running the agent again. The work touches the worker loop and close-out, the job record, retry classification, the built-in worker prompt template and the proposal. engineer is the only triage role in this repository, so it plans the steps.

## Routing
- triage:engineer: plan the change to the job record (pushed head, test result, PR body), the retryable cause, CreatePR retry with backoff, the agent-free re-open path, dry-run behaviour, GitHub-fake tests and the proposal update.
- worker engineer: Go change in internal/worker (and related effects and github fake code), plus tests and docs.

## Open issues
- The issue also asks the worker prompt to handle a branch that already holds the finished work. The worker may edit the built-in template at internal/roles/workers/engineer/prompt.txt. The deployed copy at .ard/workers/engineer/prompt.txt is a protected path, so agents may not change it. A maintainer has to sync that copy by hand.
- The change adds fields to the stored job record, and no architect role exists to review that. The engineer plan should cover compatibility with job records that already exist.
- The retry classification depends on the shared transient-error handling from the related push-retry issue. The engineer should check whether that has landed or must be built here.
