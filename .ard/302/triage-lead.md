---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Makes `ard serve` check GitHub connectivity and permissions at startup, accept a personal access token for live runs, and fall back to the `gh` CLI token when no credentials are set. It also updates the proposal, README and runbook. The work touches auth, serve startup, config check, possibly the reviewer and merger, and docs. Only the engineer triage role exists, so it plans all of it.

## Routing
- triage:engineer: implementation steps and test plan for the startup check, PAT live mode, `gh` fallback with redaction, the self-review behaviour and the doc updates.
- worker engineer: Go code changes across internal/github, internal/servecmd, internal/configcmd and possibly internal/reviewer and internal/merger, with tests against the GitHub fake and a fake `gh`, plus doc edits.

## Open issues
- The issue leaves one choice to triage: how self-review works when ard runs on a PAT. Either ard posts a COMMENT review carrying the verdict and the merger gates on ard's own record, or the docs require a second identity for reviews. The engineer plan should pick one. A maintainer may want to confirm it, because it changes the merge gate.
- The bot-commit audit workflow under .github/ should only be documented, not changed. Agents may not modify .github/, and it is a protected path.
- There are no architect, product-manager or documentation-planner roles, but this change is user-visible and crosses several packages. The engineer plan has to cover those concerns too.
