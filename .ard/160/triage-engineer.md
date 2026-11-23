---
role: engineer
status: done
attempt: 1
---
Tightens the starter eng-review prompt so the reviewer runs the repository's own lint entry point (make lint, or what the README or CI names) and falls back to its own tools only when none exists, saying so under Checks. The plan has four steps plus test updates. Size S, risk low.

## Open issues
- The live reviewer prompt under .ard/reviewers/ is protected and identical to the starter template. It is not part of this plan, so a maintainer may want to apply the same edit by hand.
