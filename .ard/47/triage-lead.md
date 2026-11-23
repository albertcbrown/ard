---
role: lead
status: needs-human
attempt: 1
---
The request is clear and its open questions are settled: add a starter lts scanner role under internal/roles. However, the only worker role here, engineer, cannot finish it, so a maintainer must decide how this work gets done before it is routed.

## Routing
None.

## Open issues
- The change depends on inputs that only a session with network access and Docker can produce: real endoflife.date API fixtures captured from the live service, and SHA-256 checksums for the pinned yq release taken from its published checksums file. The plan forbids inventing either. ard's engineer worker has no network access, so it would either stall or fabricate them.
- The change also edits merge-protected paths: the root Dockerfile, files under deploy/, and CLAUDE.md. That is allowed, but the pull request will wait for a human approval.
- Option 1: a maintainer does the work by hand, which matches how the issue itself describes the flow.
- Option 2: a maintainer first commits the real API fixtures under internal/roles/testdata/lts/api/ and the yq version and checksums, then relabels the issue for triage. ard can then route the rest (the sh and jq scanner, Go tests, proposal and CLAUDE.md updates) to triage:engineer and the engineer worker.
- Several sibling scanner issues edit the same starter lists, so whichever pull request lands second needs a small rebase.
