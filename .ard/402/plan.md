# Plan for #402

## Implementation

### Steps
1. `mkdocs.yml` (edit): add after `plugins:` a `markdown_extensions:` block with `admonition`-free minimal config: `pymdownx.superfences` with `custom_fences: [{name: mermaid, class: mermaid, format: !!python/name:pymdownx.superfences.fence_code_format}]`. No new dependency (`pymdown-extensions` is in `docs/site/requirements.txt`). The build stays green because nothing uses the fence yet.
2. `docs/site/roles/index.md` (new, nav "Roles overview"): move from `docs/site/roles.md` the intro, the directory/type table, the agent/executable/Docker paragraph, and the sections "Writing a role prompt", "The role file", "The write allowlist" verbatim. Replace "Starter roles" with a short list linking to the four kind pages (`scanners.md`, `triage.md`, `workers.md`, `reviewers.md`, relative to `roles/`). Keep the sentence that role directories are protected paths in the recommended config.
3. `docs/site/roles/scanners.md`, `triage.md`, `workers.md`, `reviewers.md` (new): each has "What it does", "Inputs and outputs", "Labels", "Starter roles" (names from the old "Starter roles" list; keep the one-line descriptions: lead routes, engineer writes the implementation plan) and "Config overrides". Source the content from the matching loop sections and starter-role paragraphs of `docs/proposal.md` and from `docs/site/configuration.md` (link to it as `../configuration.md`). Only mention config keys that exist in `docs/site/configuration.md`, because the Go drift test (`CheckKeys`) checks that page and `CheckCommands` checks every command in fenced blocks (`internal/docscheck/docscheck.go`). Any `ard ...` command in a code block must be a real CLI command.
4. `mkdocs.yml` nav: replace `- Roles: roles.md` with `- Roles:` listing `Roles overview: roles/index.md`, `Scanners: roles/scanners.md`, `Triage: roles/triage.md`, `Workers: roles/workers.md`, `Reviewers: roles/reviewers.md`.
5. `docs/site/security.md:9`: change `[Roles](roles.md)` to `[Roles](roles/index.md)`. Grep `docs/site/`, `README.md` and `docs/` once more for `roles.md` and fix any link, touching `getting-started.md`, the README and the onboarding runbook only for link fixes (PR #401 edits them).
6. `git rm docs/site/roles.md`, once steps 2 to 5 are done, so no content is lost: check that every sentence of the old file now appears in `roles/`.
7. `docs/site/index.md`: under "## The loop", before the numbered list, add a ```` ```mermaid ```` `flowchart TD` adapted from the proposal's architecture flowchart. Nodes: Issue; Scanners ("file issues"); Triage (lead, engineer, optional roles); `plan.md`; "Human `ard:approved` (when `require_approval`)"; Worker (branch, tests, draft PR); Reviewers; Merger ("reviews at head, checks green; protected paths need a human"); Merged (issue closed); `ard:needs-human`. Edges: changes returns to the worker ("review rounds, up to `max_review_rounds`"); all approve gives "PR ready" to the merger; every stage has a dashed edge to `ard:needs-human`. Keep the numbered list. Confirm `max_review_rounds` and `require_approval` appear in `docs/site/configuration.md` or the proposal; use the exact names.

### Test plan
- `make docs-check` (runs `mkdocs build --strict -d /tmp/ard-site`, then `go test ./internal/docscheck/...`). It should pass with no broken-link warnings.
- Check the built `/tmp/ard-site/index.html` contains `class="mermaid"`.
- Check the nav has Roles with five entries, and `grep -rn 'roles\.md' docs/site README.md mkdocs.yml` returns nothing.
- Failure case: if `CheckCommands` flags an invented command in the new pages, remove or correct it.
- Edge case: relative links inside `roles/*.md` need `../` to reach other pages.
- The worker's `make lint test` does not cover the docs build, so run `make docs-check` explicitly. It needs `mkdocs` (install with pip from `docs/site/requirements.txt`), and the worker has network access.

### Risk
Low. Docs and `mkdocs.yml` only; `mkdocs.yml` is not in `merge.protected_paths` and `.github/workflows/docs.yml` is untouched. A small rebase is possible against PR #401. The URL `roles.html` changes to `roles/`, so external links to the old page will break.

### Size
M (about 250 changed lines). Files: `mkdocs.yml`, `docs/site/index.md`, `docs/site/security.md`, `docs/site/roles.md` (deleted), and new `docs/site/roles/index.md`, `scanners.md`, `triage.md`, `workers.md` and `reviewers.md`.
