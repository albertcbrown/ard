---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a starter lts scanner role under internal/roles (a sh script with jq programs, a products table, a pinned tool Dockerfile, tests and proposal updates). The previous run stopped because the worker had no network to fetch the inputs. A maintainer has since committed the real endoflife.date fixtures and the yq version and checksums under internal/roles/testdata/lts/, so the rest can go to the engineer.

## Routing
- triage:engineer: turn the settled design into implementation steps and a test plan that build on the committed fixtures and pin.
- worker engineer: the scanner script, jq programs, products table, role Dockerfile, Go tests and docs updates.

## Open issues
- The committed capture record says the Alpine slug is alpine-linux, not alpine. The starter products table and the tests must use alpine-linux.
- The worker has no network or Docker. It cannot run the integration-tagged tests or the image test, and it cannot regenerate fixtures. A maintainer must run those by hand before merge. The fixture capture date, not a live fetch, fixes the expected results.
- The change touches merge-protected paths (the root Dockerfile, files under deploy/, and CLAUDE.md), so the pull request will wait for a human approval.
- Sibling scanner issues edit the same starter lists, so whichever pull request lands second needs a small rebase.
- Adding lts to the repository's own .ard/ directory is a separate change that a human must make.
