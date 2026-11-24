---
role: engineer
status: needs-human
attempt: 1
---
No code was changed. The plan spans the merger, reviewfix, worker and the committer code lane, and it adds a merge-parent commit to security-sensitive code. I judged that too large and risky to finish within this job's budget, and shipping only part of it would leave the system in a worse state.

## Open issues
- The plan itself says the code lane's new merge-parent design needs maintainer review, and the committer guards (path check, staged-change check, lease push) are security-sensitive.
- Shipping only the merger half would send ready PRs into conflict rounds that the worker cannot resolve yet. They would burn rounds until the cap.
- Suggested split, as in the plan: (a) the merger request path, plus reviewfix reason handling in `capPath` and `landing`, gated until (b) lands; (b) the code lane merge parent, the worker conflict input and the guard changes.
- The conflict-round section of the worker prompt is protected, so a maintainer must add it in a separate PR.
