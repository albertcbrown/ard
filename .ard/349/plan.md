# Plan for #349

## Implementation

Documentation only. Order from the owner decisions on #303: install the App → `ard init` → commit and merge → `ard setup` → fix manual rows → `ard serve --dry-run` → live. Do not redesign it.

### Steps

1. Read the code that the docs must match: `internal/cli/cmd_init.go` (`ard init` writes the local `.ard/` tree; `--pr` opens the `ard-init` PR instead), `internal/cli/cmd_setup.go` (`ard setup`; `--check` reports only and exits 2 on drift), `internal/setupcmd/setup.go` and `policy.go` (report rows `permissions`, `labels`, `ruleset`, `app-permissions`, `merge-settings`; statuses ok, changed, drift, manual; a manual row prints `manual: <name>: <fix>`; setup exits with "manual steps needed" until none remain) and `internal/initcmd/body.go` (merge with Squash or a merge commit, never fast-forward or rebase; nothing takes effect until merged because `ard serve` reads origin/main; the lint baseline step; the trufflehog opt-out). Also confirm `ard labels sync` still exists as a labels-only alias of `ard setup`.
2. `docs/site/getting-started.md`: rewrite the numbered sections in the new order. (1) Create and install the App (keep the permissions table and the no-`workflows` note, and the `ARD_GITHUB_*` environment). (2) `ard init`, then commit the tree on a branch and merge it by PR (or use `ard init --pr`). Keep the lock-file and rerun behaviour. (3) `ard setup`: explain it creates the labels and checks permissions, the ruleset and App bypass, App permissions and merge settings. Explain the `--check` flag and exit code 2. (4) Fix any manual rows, listing the row names above and saying each row's text is the exact fix. Rerun `ard setup` until it is green. Keep the bot-commit audit workflow (ARD_BOT_LOGIN) as a manual row-fix step. (5) Model credential. (6) `ard serve --dry-run`, then `ard serve`. (7) First issue. Drop the standalone `ard labels sync` and `ard config check` steps unless `ard config check` still exists in the CLI (check `internal/cli`). Every `ard ...` command in this file is checked by `internal/docscheck`, so use only real commands and flags, or put `<!-- docscheck:ignore -->` on the line before a deliberate exception.
3. `docs/runbooks/onboard-repo.md`: keep the title, intro and sections 6 (limits and worked example), and 8 (roles repo). Rewrite sections 1 to 5 and 7 to follow the same order: install the App; `ard init --repo <owner>/<name>`, commit and merge (Squash or merge commit); `ard setup`; fix manual rows (ruleset and bypass, bot-commit audit copy, merge settings); limits; `ard serve --dry-run` soak, then live. Renumber headings and keep the cluster note.
4. `README.md`: add a short "Getting started" section after "Configuration by convention" with the one-line flow and a link to the site page. Leave the rest as it is.
5. Link check: relative links keep working (`../proposal.md#...` in the runbook, `troubleshooting.md` on the site). Grep `docs/site` for other mentions of `ard labels sync` or the old init flow (`troubleshooting.md`, `operations.md`, `install.md`, `index.md`) and update them to match.

### Test plan

- `go test ./internal/docscheck/...` (part of `make test`): every command and flag in `docs/site/*.md` exists in the CLI. Cases to confirm by running it: `ard setup`, `ard setup --check`, `ard init`, `ard init --pr`, `ard serve --dry-run`.
- `make lint test` passes (no Go changes expected).
- `make docs-check` (needs mkdocs-material; run if available): `mkdocs build --strict` passes, so no broken links.
- Manual check: each documented row name and exit code matches `internal/setupcmd`.
- Failure case: a typo'd flag in a docs command must fail docscheck, so re-run after the final edit.

### Risk

Low. Docs only. No file in `merge.protected_paths` is touched (no `.github/`, `.ard/config.yaml`, role directories or CLAUDE.md). The main risk is drift from the code, which docscheck guards for `docs/site` but not for README or the runbook.

### Size

S. Files: `README.md`, `docs/site/getting-started.md`, `docs/runbooks/onboard-repo.md`, and possibly one-line fixes in other `docs/site/*.md` files.
