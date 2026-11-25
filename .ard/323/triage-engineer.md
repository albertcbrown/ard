---
role: engineer
status: done
attempt: 1
---
Rewrites the implementation plan for conflict rounds after the security review, now that the cover change has landed on main. The committer enforces one tree rule and builds the merge commit itself with the merge parent as second parent. It has no merge-mode retry, the worker runs no git in the agent's worktree after the agent step, and the diff guard is measured from main's tip. Size L, risk high.

## Open issues
- A maintainer should review the committer changes closely.
- The worker prompt section for conflict rounds is a separate human PR and should merge with or before this one.
- No conflict-round setting exists in the starter config. The plan wires only the merger's rounds in servecmd and leaves protected starter files to a human.
