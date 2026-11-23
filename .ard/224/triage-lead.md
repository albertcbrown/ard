---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds a built-in scanner that searches the full git history for leaked credentials, next to the existing HEAD-only trufflehog scanner under internal/roles/scanners. It also needs an executor change so scanner jobs can read the repository's history. The request is clear and its prerequisite scanner has already landed, so it goes to the engineer for planning and implementation.

## Routing
- triage:engineer: plan the executor change that gives jobs read-only access to git history (Docker now, with k8s in mind), the new scanner directory, its defaults (no autoclose, weekly cadence, own timeout, needs-human route), fingerprint parity with the HEAD scanner, and tests showing that no secret material reaches issues, logs or files.
- worker engineer: Go and scanner-script changes with unit tests.

## Open issues
- The change spans the executor and the scanner roles and decides how job containers see repository data. This repository has no architect triage role, so a maintainer should check the engineer's chosen approach (bare-clone mount or bundle) before the worker starts.
- The executor change and the new scanner could be split into two pull requests if the plan turns out large.
- The new scanner lives under internal/roles/scanners, not .ard/scanners, so it is outside the protected paths. Any configuration override for it in .ard/config.yaml would be a separate maintainer edit.
