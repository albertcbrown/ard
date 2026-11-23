---
role: engineer
status: done
attempt: 1
---
Rewrites the three credentialed-URL test fixtures to build them at run time with `url.URL` and `url.UserPassword`, adding a `net/url` import to each file. The plan has four steps and relies on the existing tests plus `make lint test`. Size S, risk low.

## Open issues
- The trufflehog scan acceptance check may not be runnable locally, so a reviewer may need to judge it from the diff.
