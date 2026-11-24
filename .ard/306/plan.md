# Plan for #306

## Implementation

### Steps
1. New `.goreleaser.yaml` (repo root, `version: 2`):
   - `project_name: ard`; one `builds` entry: `main: ./cmd/ard`, `binary: ard`, `env: [CGO_ENABLED=0]`, `goos: [darwin, linux]`, `goarch: [arm64, amd64]`, `flags: [-trimpath]`, `ldflags: ["-s -w -X main.version={{.Tag}}"]` (same variable as `Makefile` LDFLAGS and `var version` in `cmd/ard/main.go:18`), `mod_timestamp: "{{ .CommitTimestamp }}"`.
   - `archives`: `formats: [tar.gz]`, name template `ard_{{ .Version }}_{{ .Os }}_{{ .Arch }}`.
   - `checksum: { name_template: checksums.txt }`.
   - `release: { mode: keep-existing, github: { owner: albertcbrown, name: ard } }`.
   - `homebrew_casks` entry: `name: ard`, `repository: { owner: albertcbrown, name: homebrew-tap, token: "{{ .Env.TAP_GITHUB_TOKEN }}" }`, `directory: Casks`, `url.template` only under `https://github.com/albertcbrown/ard/releases/download/{{ .Tag }}/{{ .ArtifactName }}`, `dependencies: [{ formula: git }]`, `caveats` (agents need `claude` and `CLAUDE_CODE_OAUTH_TOKEN` or an API key), `hooks.post.install` running `system_command "/usr/bin/xattr", args: ["-dr", "com.apple.quarantine", "#{staged_path}/ard"]`. Verify exact key names against the GoReleaser v2 docs for the pinned version; the generated cask must have per-arch `sha256` and never `:no_check`. Env var name is a placeholder the later workflow PR supplies; no secret value in the file.
2. New `deploy/test-release.sh` (executable, `set -euo pipefail`, like the other `deploy/test-*.sh`): fail with a clear message if `goreleaser` is missing; run `goreleaser check`; run `goreleaser release --snapshot --clean --skip=publish`; assert `dist/` has an `ard` binary for each of darwin/linux x arm64/amd64 (read `dist/artifacts.json`) and `checksums.txt`; run the host-matching binary with `version` and require the output to equal `ard <tag>` where tag is `.tag` in `dist/metadata.json` (what `{{.Tag}}` expanded to); assert the generated cask under `dist/homebrew/Casks/ard.rb` contains no `:no_check` and every URL starts with `https://github.com/albertcbrown/ard/releases/`; run `brew audit --cask` on it only when `brew` exists, otherwise print a skip notice.
3. `Makefile`: add `release-check` to `.PHONY` and a target with a comment (needs goreleaser, not part of `make test`) that runs `deploy/test-release.sh`.
4. `docs/layout.md`: add `.goreleaser.yaml` and `deploy/test-release.sh` lines to the layout block, mention `make release-check`.

### Test plan
- `make test` still passes (worker test command); `make lint`.
- `make release-check` with goreleaser installed: builds all four targets; the version assertion passes; the cask has per-arch sha256.
- Failure cases: goreleaser absent gives a non-zero exit and an install hint; a missing target in `dist/artifacts.json` fails; a mismatched `ard version` fails.
- `grep -iE 'token|secret' .goreleaser.yaml` shows only the `{{ .Env.* }}` reference.
- `brew audit --cask`: a cask with a local path may need `brew audit --cask --new` or a tap context; if it cannot run cleanly in CI, document that in the script header and the PR. Goreleaser and brew are not installed in the worker sandbox (goreleaser is absent here), so the worker should state which checks it could not run.

### Risk
Low. No Go code, no `.github/` or protected paths change. `.goreleaser.yaml` governs released artifacts but is not in merge.protected_paths; a maintainer may want to protect it. The postflight quarantine removal is an owner-accepted risk. The `{{.Tag}}` snapshot value depends on the nearest tag, hence the metadata-based assertion.

### Size
M (about 120 changed lines). Files: `.goreleaser.yaml` (new), `deploy/test-release.sh` (new), `Makefile`, `docs/layout.md`.
