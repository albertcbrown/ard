---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Lets a live `ard init --pr` run with a personal access token or the gh CLI fallback, using the same token sources `ard serve` already accepts. It also rewords the init PR body's follow-up section for both auth kinds and updates the docs. The work stays in the init command, its tests and two docs, so the engineer can plan it alone.

## Routing
- triage:engineer: reuse serve's token resolution in the init auth check, adjust the PR body wording, add table tests for App, token and gh fallback, and list the doc edits (proposal and onboard-repo runbook).
- worker engineer: code, test and doc change in the init command.

## Open issues
- No product-manager, architect, designer or documentation-planner triage roles exist in this repository, so the engineer's plan must cover the user-visible wording and doc updates too.
- ard must still never write `.github/` in the init PR, even when a token could. The plan should keep that guarantee and test it.
