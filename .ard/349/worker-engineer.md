---
role: engineer
status: done
attempt: 1
---
Rewrites the onboarding docs around the new order: install the App, `ard init`, commit and merge, `ard setup`, fix manual rows, `ard serve --dry-run`, then live.

## Changes
- docs/site/getting-started.md: new sections for init (local tree, `--pr`, merge by Squash or merge commit), setup (rows, `--check`, exit 2) and manual-row fixes, including the bot-commit audit.
- docs/runbooks/onboard-repo.md: sections 1 to 6 follow the same order and are renumbered; the limits example and roles repo section are unchanged.
- README.md: short "Getting started" section with the one-line flow.
- docs/site/index.md, troubleshooting.md: `ard labels sync` mentions now point to `ard setup`.

## Testing
Ran `go test ./internal/docscheck/...`; it passes. Did not run the full `make lint test` or `make docs-check`.

## Open issues
- `ard config check` still exists, so the other pages that mention it are unchanged.
- `ard labels sync` is still mentioned in docs/site/operations/gate-2-runbook.md, left as it is.
