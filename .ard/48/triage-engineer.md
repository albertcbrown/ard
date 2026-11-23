---
role: engineer
status: done
attempt: 1
---
Adds a starter lint scanner role (shell script, jq normaliser and pinned Dockerfile) with golden and stubbed tests, starter config, init PR body text, a Makefile target and docs updates, in 13 ordered steps. Size L, risk medium.

## Open issues
- The worker has no Docker or network, so a human must verify the Dockerfile pins and run the fixture regeneration target. The real golangci-lint fixtures will be hand-built until then.
- The change edits CLAUDE.md, a protected path, so the PR needs human approval.
- Enabling the scanner in this repository's own .ard/ directory, with its baseline, is a separate human PR. The follow-up issues named in the issue should be filed by a maintainer.
