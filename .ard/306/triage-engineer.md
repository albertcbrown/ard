---
role: engineer
status: done
attempt: 1
---
Adds a GoReleaser v2 config with the Homebrew cask, a `deploy/test-release.sh` script behind a new `make release-check` target, and layout doc entries. Size M, risk low: no Go code or protected paths change.

## Open issues
- goreleaser is not installed in the planning environment, so the key names in the cask block must be checked against the pinned GoReleaser version, and `make release-check` and `brew audit --cask` may not be runnable by the worker.
- The tap token env var name is a placeholder that the later release workflow must supply.
- A maintainer may want to add `.goreleaser.yaml` to merge.protected_paths.
