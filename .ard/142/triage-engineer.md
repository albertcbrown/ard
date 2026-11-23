---
role: engineer
status: done
attempt: 1
---
Refuses any worker change to a `.gitattributes` file in the committer's shared code-path check, so the job is held for a human, and names that rule in the prompt and design doc. Size S, risk low.

## Open issues
- The hold is in code rather than in the merger's protected paths, since that list is human-owned and has no code floor. Maintainers who also want it in `merge.protected_paths` must edit the config by hand.
- Workers can no longer make legitimate `.gitattributes` edits without a human.
