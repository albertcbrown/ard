---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a second, optional pass to the close-out secret scan: a new scanner in the redact package that runs a pinned gitleaks binary, turned on by configuration. It fails closed and handles a hit the same way as an internal scan hit. The existing Scanner interface already supports more scanners, so the engineer can plan it directly.

## Routing
- triage:engineer: steps for the gitleaks Scanner, the config switch, wiring into close-out, fake-binary unit tests, the integration test with the real pinned binary, and the Guardrails update in docs/proposal.md.
- worker engineer: the code, test and doc change.

## Open issues
- This adds a new external binary dependency and a new configuration key. This repository has no architect or documentation-planner triage role, so the engineer's plan has to cover the config schema and the doc updates.
- Pinning gitleaks where the real integration test and production can reach it may mean changing the Dockerfile, Jenkinsfile or deploy/. Those are protected paths, so a pull request that touches them waits for a human approval.
- The issue lists #20 as a dependency. Close-out redaction appears to have landed already (commits for #21 and #166 are on main), but a maintainer should confirm #20 is complete.
