---
role: engineer
status: done
attempt: 1
---
Plans a new starter lts scanner role (sh script, two jq programs, a products table and a pinned tool Dockerfile) with Go tests, built on the committed fixtures and yq pin. Size L, risk medium: it touches merge-protected paths and a new pinned binary, and any data gap must fail closed so issues are not wrongly autoclosed.

## Open issues
- The embed directive in internal/roles/roles.go lists role directories by name, so scanners/lts must be added there. The design text assumed a glob.
- The Alpine product slug is alpine-linux, per the capture record.
- The worker cannot run the integration-tagged tests or the image test; a maintainer must run them before merge.
- The pull request touches the root Dockerfile, deploy/ and CLAUDE.md, so it waits for a human approval.
