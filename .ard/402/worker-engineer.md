---
role: engineer
status: done
attempt: 1
---
Adds a Mermaid process diagram to the docs overview and splits the single roles page into a roles overview plus one page per role kind, so maintainers find kind-specific inputs, outputs, labels and config in one place.

## Changes
- `mkdocs.yml`: `pymdownx.superfences` with a `mermaid` custom fence; the nav now has a Roles section with Roles overview, Scanners, Triage, Workers and Reviewers.
- `docs/site/index.md`: flowchart under "The loop" (scanners, triage, plan, approval, worker, reviewers with the review-fix loop, merger, `ard:needs-human` exit); numbered list kept.
- `docs/site/roles/index.md`: the intro, directory table, and the prompt, role file and write allowlist sections moved verbatim from the old page, plus links to the four kind pages.
- `docs/site/roles/{scanners,triage,workers,reviewers}.md`: what it does, inputs and outputs, labels, starter roles, config overrides (only keys in the configuration page).
- `docs/site/roles.md` removed; the link in `docs/site/security.md` updated. Nothing else linked to it.

## Testing
Ran `make docs-check` (mkdocs build --strict plus the Go docscheck test): passes. The built overview contains a `mermaid` block. `grep` for `roles.md` in docs/site, README and mkdocs.yml finds nothing.

## Open issues
- The old `roles.html` URL no longer exists, so external links to it break.
- The old "Starter roles" list is now split across the kind pages, with the one-line descriptions for lead and engineer kept.
