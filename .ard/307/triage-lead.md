---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a docs-only change. It adds a Homebrew install section to the README, extends the release runbook and updates the proposal's Delivery, Rollout and Guardrails sections. The dependency it names, part A (#306, the GoReleaser config and release workflow), is already merged on main, so the work can start now. Only the engineer planner and worker are needed.

## Routing
- triage:engineer: list the exact sections to add or edit in README.md, docs/runbooks/release.md and docs/proposal.md, and check them against the merged GoReleaser config and release workflow so the docs describe what actually ships.
- worker engineer: write the documentation changes. No code changes.

## Open issues
- docs/runbooks/release.md already exists and is shared with another issue (#247). The worker should extend it, not rewrite it.
- The proposal will record the Gatekeeper quarantine-clearing decision as an accepted risk. A maintainer should confirm that wording when reviewing the pull request.
- No documentation-planner or architect role exists in this repository, so the engineer planner covers the doc structure as well.
