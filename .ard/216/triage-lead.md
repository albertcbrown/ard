---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds the trufflehog starter scanner role under internal/roles/scanners with its starter config override, the route set to needs-human, and the starter listing tests. The dependency on issue 215 looks satisfied: the route key and its needs-human value already exist in internal/config on main. The work can start now.

## Routing
- triage:engineer: confirm the plan against current main. Check that the 215 route API matches what the plan assumes, and lay out the steps and the fake-binary and canary test plan.
- worker engineer: new scanner role files (shell and jq), the starter config override, the PR-body note and test updates in internal/roles and internal/initcmd.

## Open issues
- This code handles live secrets. A projection bug could publish a credential in a public issue, so a maintainer should read the scan script and the jq filter line by line before merge.
- The plan updates CLAUDE.md, which is a protected path. That PR will wait for a human approval.
- TruffleHog is AGPL-3.0. The plan keeps it out of the published image, and the reviewer should confirm that nothing adds it to the root Dockerfile.
