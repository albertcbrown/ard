---
role: engineer
status: done
attempt: 1
---
Adds an optional `fetched:` list to worker role files, parsed strictly in `internal/closeout` and carried on the worker result. The worker PR body renders it as a Fetched inputs list, and the proposal's egress and network text is corrected. The plan has nine steps with table tests for each invalid field. Size M to L, risk low to medium.

## Open issues
- The issue refers to seven risks listed in the tracking issue. That text is not in the repository, so the worker needs it from a maintainer, or the proposal wording needs review.
- The issue's line references (~267, ~319, ~444) do not match the current proposal. The plan names the egress text found at ~283, ~335 and ~490, and the worker should grep for all egress claims.
- No CAPTURE.md is added, since none exists under the fixture directory today; the proposal only documents the convention.
- The proposal's network text should match what the code enforces now. The network setting is advisory in the config part; the worker must confirm what the Docker executor enforces before describing it.
