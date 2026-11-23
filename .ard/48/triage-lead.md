---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a starter lint scanner role under internal/roles/scanners/lint (scan script, jq normaliser, pinned Dockerfile), with fixtures, tests, starter config and docs updates. It follows the layout of the existing cve, docs and error-codes starter scanners, its only dependency (the scanner pipeline) is closed, and the owner has settled the open design questions, so it goes to the engineer for implementation steps.

## Routing
- triage:engineer: turn the existing plan into ordered steps and a test plan for the scan script, normalize.jq, Dockerfile, starter config, init PR body, Makefile target and golden and stubbed tests.
- worker engineer: code, tests and docs change within internal/roles, internal/initcmd, the Makefile and docs.

## Open issues
- The worker has no Docker or network, so it cannot verify the Dockerfile pins or regenerate the real golangci-lint fixtures. A human must run the fixture regeneration target and check the pins before merge.
- The change also edits CLAUDE.md (layout lines), which is a protected path, so the PR will wait for human approval.
- Enabling the scanner in this repository's own .ard/ directory (copying the role, committing a baseline, editing .ard/config.yaml) is a separate human-only PR and is out of scope for the worker.
- The issue asks for follow-up issues to be filed (shellcheck, toolchain switching, count-aware baseline, the human .ard/ PR); a maintainer should file those.
