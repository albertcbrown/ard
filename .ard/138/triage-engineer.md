---
role: engineer
status: done
attempt: 1
---
Adds an optional merge hook to committer files so close-out commits only the job's own plan.md section onto the latest main copy, and fails closed to needs-human if the file cannot be merged. The plan has six steps, tests including a regression case where another role's section changes on main, and a proposal doc update. Size M, risk medium because the shared committer file type and retry loop change.

## Open issues
- The close-out error branch in step 5 may need to reuse an existing helper that keeps only the log and role file. The worker should check how the allowlist-denial path in prepare.go does this.
- A maintainer may want to confirm the rebase-over-conflict approach during review.
