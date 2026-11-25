# Plan for #307

## Implementation

The release workflow (`.github/workflows/release.yml`) and `.goreleaser.yaml` are now on main, so the earlier blocker is gone. `docs/proposal.md` Delivery (the Homebrew bullet) and the Phase 4/5 Rollout diagram already exist, and `docs/runbooks/release.md` already has a "Homebrew release" section. The work is the remaining gaps only. Docs only; no code, no `.github/`, no `.ard/` changes.

### Steps

1. `README.md`: add a new `## Install` section between "How it works"/"Configuration by convention" and `## Development` (before `## Development`). Content:
   - `brew install --cask albertcbrown/tap/ard`, `brew upgrade --cask albertcbrown/tap/ard` (or `brew upgrade`), `ard version` to check.
   - `brew uninstall --cask ard` removes the binary only; `~/.ard/` (state, locks, pause files) is left alone, delete it by hand if wanted.
   - Note the cask depends on the `git` formula, and that the `claude` CLI and a model credential are still needed (matches `caveats` in `.goreleaser.yaml`).
   - Note the binary is not yet signed or notarized, so the cask clears the macOS quarantine flag after install (`hooks.post.install` in the cask); link to the proposal Guardrails.
   - Building from source still works: `make build` (existing Development section), plus link to `docs/site/install.md`.
2. `docs/site/install.md`: replace the last sentence ("A Homebrew cask is planned (#177)...") with a short "Homebrew" subsection (same command, upgrade, uninstall note) placed before "Build from source". Keep the build instructions.
3. `docs/runbooks/release.md`: extend, do not rewrite (shared with #247). Keep all existing sections. Add:
   - An overview paragraph/section "How a merge becomes a release" after the intro: merge to `main` -> `version.yml` tags `vX.Y.Z` and creates a notes-only release -> `release.yml` (on the `v*` tag push) runs GoReleaser, uploads four archives and `checksums.txt` to that release (`release.mode: keep-existing`) -> GoReleaser pushes the cask `Casks/ard.rb` to `albertcbrown/homebrew-tap`. Mention the install check: `brew update && brew install --cask albertcbrown/tap/ard`, `ard version`.
   - Extend "## Recovery" with a "Failed release.yml" list: rerun the failed run (it is idempotent for assets? verify in `.goreleaser.yaml`/release.yml `release.mode` and `--clean` flags before wording); if the tag push run is superseded, use `workflow_dispatch` with `tag`; if the tap push fails, check `TAP_GITHUB_TOKEN` scope/expiry and rerun; never delete or move a tag; a bad release is fixed forward with the next version.
   - State the concurrency rule explicitly under that section heading: one release at a time, `concurrency: release` with `cancel-in-progress: false`, so a second run queues rather than cancelling an upload.
   - Add a "Gatekeeper" note: quarantine is cleared in the cask's `hooks.post.install` (not `postflight`; use the config's wording); accepted risk; notarization is the follow-up.
   Verify every name against `release.yml` and `.goreleaser.yaml` before writing.
4. `docs/proposal.md`:
   - Guardrails (`## Guardrails`, after the "tag-writing credential is separate" bullet, ~line 526): add a bullet "**Gatekeeper: unsigned binary, quarantine cleared (accepted risk).**" saying the release binary is neither signed nor notarized, the cask removes `com.apple.quarantine` from the installed binary in `hooks.post.install`, this skips Gatekeeper's check so the install path trusts the release archive and its checksum (and the tap token holder), the owner accepts it, and Apple notarization (Developer ID signing in the release workflow) is the follow-up. Flag the wording for maintainer confirmation in the PR body.
   - Rollout (~line 582): Phase 5 sentence exists. Expand lightly only if it lacks the install docs: say Phase 5 ships the README Install section and release runbook. Do not touch the mermaid diagram.
   - Delivery (~line 565): already complete; only add a pointer "Install instructions: README" if useful. No other change.

### Test plan

- Command: `make lint test` (docs-only; confirms nothing else broke). If a docs link or markdown check exists in the Makefile/CI, run it (`grep -n docs Makefile`).
- Manual checks (the worker reports these in the PR):
  - Every command, file, secret, variable and workflow name in the new text appears verbatim in `release.yml`, `version.yml` or `.goreleaser.yaml` (`grep` each).
  - README and the runbook say `hooks.post.install`, not `postflight`.
  - README link anchors resolve (`docs/proposal.md#guardrails`).
  - `git diff --stat` shows only `README.md`, `docs/site/install.md`, `docs/runbooks/release.md`, `docs/proposal.md`; existing runbook sections are unchanged apart from additions.
  - Run `make release-check` only if goreleaser is installed locally; it is not required.
- Failure case: if docs tests (e.g. in `docs/site` build or `internal/` tests that read docs) fail, fix wording, not the tests.

### Risk

Low. Docs only; none of the protected paths (`.ard/`, `.github/`) are touched. No public interface or migration. The only sensitive part is the Gatekeeper accepted-risk wording, which a maintainer should confirm in review.

### Size

S to M (roughly 60 to 120 changed lines). Files: `README.md`, `docs/site/install.md`, `docs/runbooks/release.md`, `docs/proposal.md`.
