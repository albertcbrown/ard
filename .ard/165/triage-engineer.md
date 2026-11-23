---
role: engineer
status: done
attempt: 1
---
Adds a git version parser and check to `internal/gitx`, and calls it at the start of `ard serve` and `ard config check` so both refuse to run on an older git. The error names the version found and the minimum. Size S to M, risk low.

## Open issues
- The exact minimum is not pinned. Only 2.47.3 (passes) and 2.39.5 (fails) are known, and no intermediate git was available to bisect. The plan uses 2.47.0 as a conservative constant. A maintainer should confirm it, or lower it after running the gitx and committer tests on intermediate versions.
- Making the failing fetch work on older git is left as a separate follow-up.
