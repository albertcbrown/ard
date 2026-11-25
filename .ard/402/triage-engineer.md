---
role: engineer
status: done
attempt: 1
---
Plans a docs-only change: enable the Mermaid fence in `mkdocs.yml`, add a process diagram to the overview, and replace the single roles page with an overview plus one page per role kind. It also updates the nav and the one link to the old page. Size M, risk low.

## Open issues
- The worker's test command does not run the docs build, so the plan has it run `make docs-check` directly.
- The old roles page URL goes away, so external links to it will break.
- A small rebase is possible against the open PR that edits the getting-started page.
