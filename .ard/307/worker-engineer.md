---
role: engineer
status: done
attempt: 1
---
Documents the released Homebrew install path: a README Install section, the merge-to-cask flow and failure recovery in the release runbook, and the Gatekeeper decision as an accepted risk in the proposal.

## Changes
- README.md: new Install section (cask install, upgrade, uninstall leaves `~/.ard/`, git/claude requirements, quarantine note, build from source).
- docs/site/install.md: Homebrew section; removed the "cask is planned" sentence.
- docs/runbooks/release.md: "How a merge becomes a release" (with the one-release-at-a-time rule), "A failed release.yml run" and "Gatekeeper" under Recovery. Existing sections are unchanged; one-time setup is not repeated.
- docs/proposal.md: Guardrails bullet for the Gatekeeper accepted risk, notarization as the follow-up; Phase 5 text mentions the README Install section and the runbook.

## Testing
Docs only. Names (`keep-existing`, `--clean`, `concurrency: release`, `TAP_GITHUB_TOKEN`, `hooks.post.install`) were checked against `release.yml` and `.goreleaser.yaml`. `make lint test` timed out after 2 minutes here, so it did not complete; no code changed.

## Open issues
- A maintainer should confirm the Gatekeeper accepted-risk wording in the proposal.
- The docs say `hooks.post.install`, matching the config, not `postflight`.
- The runbook's Homebrew section has bullets after tap setup step 5 that look misplaced; left as is.
