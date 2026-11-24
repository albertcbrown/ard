---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This change edits the starter role prompts embedded in internal/roles so each one names the repo context docs it reads, and adds the rules for absent files, precedence and conflicts. It also adds a table-driven test and updates the matching paragraphs in docs/proposal.md. Part A, the dependency, has already landed on main, so the work can start now. This repository has only the engineer triage role, so the engineer plans the work alone.

## Routing
- triage:engineer: list the prompt edits for each starter role, the shape of the table-driven test in internal/roles and the docs/proposal.md paragraphs to update.
- worker engineer: change the prompt text, add the test and update the docs. The work stays inside internal/roles and docs/.

## Open issues
- The change edits only the embedded starter prompts under internal/roles. It must not touch the live prompts under .ard/triage/, .ard/workers/ or .ard/reviewers/, which are protected paths. A maintainer syncs those in a separate PR.
- The product-manager, architect and documentation-planner triage roles are not installed in this repository, so nobody reviews the prompt wording for product or architecture. The design is already set in the parent issue, so this is a small risk.
