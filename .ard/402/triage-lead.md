---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a docs-site-only change: add a Mermaid process diagram to the overview page, and split the single roles page into an overview plus one page for each role kind. The issue already sets out the design and the acceptance criteria, so the engineer plans the steps and the engineer worker writes the pages.

## Routing
- triage:engineer: plan the file moves, the mkdocs.yml nav and Mermaid fence settings, the link fixes (security.md links to the roles page today) and the `make docs-check` run.
- worker engineer: edit Markdown and mkdocs.yml under the docs site. No Go code changes are expected.

## Open issues
- An open pull request for another issue is editing getting-started.md, the README and the onboarding runbook. This change should touch those files only to fix links, and may need a small rebase.
- The workflow under .github/ that builds the site is a protected path and should not change. The plan must keep the edits to mkdocs.yml and docs/site/.
- The worker's test command (make lint test) does not run `mkdocs build --strict`. A maintainer may want to confirm the docs check passes in CI before merging.
