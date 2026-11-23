---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the second part of the missing-tests starter scanner. It adds the scan script, a pinned Dockerfile, the starter config entry, the test lists and the docs. The Go helper it depends on (part 1, issue 212) has already landed in internal/roles/scanners/missing-tests, and the design was settled in the parent issue. The work follows the existing cve, docs and error-codes starter scanners, so only the engineer's plan is needed.

## Routing
- triage:engineer: steps for the scan script, the Dockerfile, the starter config entry, the roles and initcmd test lists, the end-to-end and integration tests, and the docs edits.
- worker engineer: code, tests and docs change inside internal/roles and docs.

## Open issues
- The package layout moved out of CLAUDE.md into docs/layout.md after this issue was filed. The starter-role list now lives on the internal/roles line of docs/layout.md, so the planner should update that file instead of CLAUDE.md. If CLAUDE.md is touched after all, it is a protected path and a human must approve the merge.
- The change adds a Dockerfile under internal/roles/scanners/missing-tests. The protected Dockerfile entry in the config is meant for the root Dockerfile, and a maintainer should check whether this one also triggers the approval gate.
- Turning the scanner on in this repository's own .ard/ is out of scope, and only a human may make that change.
