---
role: engineer
status: done
attempt: 1
---
The release workflow and GoReleaser config are now on main, so the earlier blocker is gone. The plan adds a README Install section, updates the site install page, extends the release runbook with the merge-to-cask flow, failure recovery and the one-release-at-a-time rule, and adds the Gatekeeper accepted risk to the proposal's Guardrails. Size S to M, risk low.

## Open issues
- The proposal's Delivery section, Phase 5 text and the runbook's Homebrew section already exist, so the plan covers only the gaps.
- The docs must say the cask clears quarantine in `hooks.post.install`, not `postflight`, to match the config.
- A maintainer should confirm the Gatekeeper accepted-risk wording in the pull request.
