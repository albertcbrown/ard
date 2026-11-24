---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a GoReleaser configuration with a Homebrew cask definition and a local make target that runs a snapshot release check, plus a layout doc update. The decisions are already owner-approved and the scope is a self-contained build config, so only engineering planning is needed before the engineer implements it.

## Routing
- triage:engineer: steps for the GoReleaser config, the cask block, the release-check make target and its version assertion, and how to handle the brew audit criterion.
- worker engineer: add the config file, Makefile target and docs/layout.md entry.

## Open issues
- The version ldflags the issue depends on already appear in the Makefile, so the dependency looks satisfied; the engineer should confirm the variable name matches.
- The worker's test command is make test; release-check needs goreleaser (and brew audit needs Homebrew), which may not be installed where the worker runs, so the plan should say how that criterion is verified.
- The new root file is not under merge.protected_paths, though it governs what gets released; a maintainer may want to consider protecting it later (agents may not change config.yaml).
