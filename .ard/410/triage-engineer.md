---
role: engineer
status: done
attempt: 1
---
Rewrites the flaky search-spacing test so it no longer measures from the wrong instant. The burst and non-search checks use a long-interval limiter and a short context deadline, and the spacing check asserts only a lower bound from the start. Size S, risk low; no production code changes.

## Open issues
None.
