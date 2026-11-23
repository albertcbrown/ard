---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a clear hardening change. The scanner pipeline in internal/scanner will neutralise issue titles in one place, the starter scanners under internal/roles/scanners will share an inline-code rule for paths and identifiers in bodies, and the scanner contract in docs/proposal.md will state the rule. The engineer plans the steps and the table tests, and the engineer worker implements them.

## Routing
- triage:engineer: plan the central title sanitiser in the scanner pipeline (next to the existing marker handling), the shared body helper for the starter scanners, the hostile-input table tests, and the proposal update.
- worker engineer: code, tests and the docs change.

## Open issues
- The repository's own scanner copy under .ard/scanners/ is a protected path. If the change has to update it as well as the starter templates under internal/roles, a maintainer must approve the pull request. Agents may not edit that copy, so it may need a separate human follow-up.
- No architect or documentation-planner role exists. The engineer's plan has to cover both the cross-scanner contract and the proposal wording.
