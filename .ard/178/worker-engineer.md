---
role: engineer
status: done
attempt: 1
---
Adds user documentation as an MkDocs Material site under docs/site/, Go checks that fail when the docs name a CLI command, flag or config key that does not exist, and a README link to the site. The Pages workflow itself is a human step under .github/, described in a new runbook.

## Changes
- docs/site/: overview, install, getting started, configuration reference, roles, operations, security model and troubleshooting pages, plus requirements.txt pinning mkdocs. The Gate 2 runbook moved to docs/site/operations/gate-2-runbook.md; the proposal's link to it is updated.
- mkdocs.yml (strict build, search, nav) and `/site/` in .gitignore.
- internal/config/keys.go: `Keys()` lists every documented config key; a test checks it against the rendered config and role override fields.
- internal/cli/commands.go: `CommandPaths()` lists command paths and their flags from the real command tree.
- internal/docscheck/: checks commands and flags in docs code spans and fences, and that configuration.md's key table matches `Keys()` exactly. `<!-- docscheck:ignore -->` opts a line out.
- Makefile `docs-check` target; README documentation link and status line; docs/layout.md entries; go.mod marks pflag as a direct dependency (the CLI helper imports it).
- docs/runbooks/pages-setup.md: the exact .github/workflows/docs.yml and Pages settings a maintainer must add.

## Testing
Ran `go test` on internal/docscheck, internal/cli, internal/config and internal/botcheck: all pass. The new tests cover bad command, bad flag, ignore marker, undocumented and stale keys, and the real docs.

## Open issues
- `make lint` and the full `make test` could not complete here: `go vet ./...` fails on a pre-existing broken fixture (internal/roles/testdata/docs/skips/src/_x.go) and needs network for x/tools. gofmt and go vet pass on the changed packages.
- mkdocs is not installed here, so `mkdocs build --strict` was not run.
- Not done, needs a human: add the docs workflow and enable Pages (docs/runbooks/pages-setup.md), and follow Getting started by hand on a scratch repo. The proposal's Rollout already lists Phase 4, so it was not edited.
- Some operations details (upgrade steps, clone rebuild) are written from the proposal and code reading and should be checked in the hand run.
