---
role: engineer
status: needs-human
attempt: 1
---
No plan was written. The docs would have to describe a release workflow that runs the GoReleaser config, and no such workflow is on the default branch. Agents cannot add one because workflow files live under .github/, which is protected.

## Open issues
- `.goreleaser.yaml` and `make release-check` are merged, but `.github/workflows/` holds only `bot-commits.yml`, `ci.yml` and `version.yml`. None of them runs GoReleaser. The config's own header comment refers to a release workflow, and the cask needs a `TAP_GITHUB_TOKEN` in that workflow's environment. Without the workflow, nothing attaches archives or checksums to a release or bumps the cask.
- A maintainer should add that workflow by hand, or confirm it is tracked elsewhere. They should also confirm that the `albertcbrown/homebrew-tap` repository and the token secret exist.
- After that, the docs work is small (size S to M, risk low, no protected paths):
  - README: add an Install section covering the cask command, `brew upgrade`, an uninstall that leaves `~/.ard/` alone, and building from source.
  - `docs/runbooks/release.md`: extend it, since #247 shares it, with the tag-to-release-to-cask flow, failure recovery and the one-release-at-a-time rule.
  - `docs/proposal.md`: update the Delivery, Phase 5 (already one sentence after the Rollout diagram) and Guardrails sections, recording the Gatekeeper quarantine clearing in `postflight` as an accepted risk, with notarization as the follow-up.
- The config uses a cask `hooks.post.install` stanza that clears quarantine, not a `postflight` block. The docs should use wording that matches the config.
