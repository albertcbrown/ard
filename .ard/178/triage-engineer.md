---
role: engineer
status: done
attempt: 1
---
Plans MkDocs Material user docs under docs/site/, two small helpers that list the CLI commands and config keys, a Go test that fails when the docs name a command or key that does not exist, a Makefile target, and README and layout updates. Size L, risk low because no runtime behaviour changes and no protected path is touched.

## Open issues
- The Pages deploy workflow, the CI steps for the site build and link check, and enabling Pages under settings need a maintainer under .github/. The plan puts the exact contents in a new runbook, docs/runbooks/pages-setup.md.
- Following Getting started by hand on a scratch repository must be done by a human before the issue is complete.
- The change is mostly Markdown and near the large-change warning level. A maintainer may split it into docs content first and then the site and checks.
- MkDocs Material was chosen as the generator without a product-level review.
