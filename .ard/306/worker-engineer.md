---
role: engineer
status: done
attempt: 1
---
Adds a GoReleaser v2 config with a Homebrew cask and a `make release-check` target, so the release build and the cask can be verified locally before a release workflow exists.

## Changes
- `.goreleaser.yaml`: CGO off, `-trimpath`, `-s -w -X main.version={{.Tag}}`, commit-timestamp mod time; darwin/linux x arm64/amd64 tar.gz archives and `checksums.txt`; `release.mode: keep-existing`; `homebrew_casks` for the tap (`Casks/ard.rb`) with release-only URLs, `git` dependency, a caveat about `claude` and credentials, and a postflight `xattr -dr com.apple.quarantine`. The tap token is only a `{{ .Env.TAP_GITHUB_TOKEN }}` reference (placeholder name for the later workflow).
- `deploy/test-release.sh`: runs `goreleaser check` and a snapshot release; asserts all four binaries and `checksums.txt`, that the host binary's `ard version` equals `ard <tag from dist/metadata.json>`, that the cask has no `:no_check` and only release URLs; runs `brew audit --cask` when brew exists.
- `Makefile`: `release-check` target. `docs/layout.md`: new root file and script.

## Testing
`bash -n` on the script; `go test` for `internal/docscheck` and `cmd/ard` pass. `make release-check` was NOT run: goreleaser is not installed here.

## Open issues
- Cask key names (`hooks.post.install`, `dependencies`, `url.template`) and the `brew audit --cask` result are unverified against a real goreleaser run; run `make release-check` once with goreleaser v2. brew audit on a path may need a tap context.
- The full `make test` timed out in the sandbox (2 minute limit); only the touched-area packages were run. No Go code changed.
- Consider adding `.goreleaser.yaml` to merge.protected_paths.
