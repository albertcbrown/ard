---
role: engineer
status: done
attempt: 1
---
Adds user documentation as an MkDocs Material site under docs/site/, with Go checks that keep the docs in step with the CLI and config keys. This round corrects the documented worker diff limits after the merge of main.

## Changes
- docs/site/: overview, getting started, configuration, roles, operations, security, troubleshooting and install pages; the Gate 2 runbook moved in.
- mkdocs.yml, requirements, Makefile `docs-check` target, .gitignore, README and layout doc updates.
- internal/config/keys.go, internal/cli/commands.go: key and command lists for the drift checks.
- internal/docscheck: tests that verify commands, flags and config keys named in the docs.
- docs/runbooks/pages-setup.md: the workflow and Pages settings a maintainer must add under .github/.

## Testing
Earlier rounds: go test on docscheck, cli and config passed. This round only edited two table cells in docs/site/configuration.md; no code changed, tests not rerun. mkdocs is not installed here, so the site build was not run.

## Review findings
- docs/site/configuration.md line 241 (reported): fixed: `diff_warn_lines` now 2000 and `diff_max_lines` 3000, matching internal/config/defaults.go. They sit in the table at lines 95 and 96.
- docs/site/roles.md line 540: not changed: reviewer said no change needed.
- internal/config/keys.go line 8: not changed: low severity; Keys() stays a hand-written list guarded by the raw-parser test.
- internal/docscheck/docscheck.go line 73: not changed: low severity; backslash continuations are not joined.

## Open issues
- The Pages workflow and the Pages setting are a human step (see docs/runbooks/pages-setup.md).
- The manual scratch-repo walkthrough of Getting started is still to be done by a human.
