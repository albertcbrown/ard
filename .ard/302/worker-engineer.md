---
role: engineer
status: done
attempt: 1
---
`ard serve` now checks GitHub connectivity and permissions before any loop starts, accepts a personal access token for live runs, and falls back to the `gh` CLI token (memory only) when no credentials are set.

## Changes
- `internal/github/connect.go`, `errors.go`: `Connect` checks the repo and permissions (App installation permissions; token repo permissions, `X-OAuth-Scopes`, and issue/PR probes for fine-grained tokens) and returns a one-line `ConnectError`.
- `internal/github/ghcli.go`: `GhFallback` (absent, logged out, logged in); errors never carry the token or gh's stderr.
- `internal/github/auth.go`: `TokenAuth` has a `Source` and never formats its token with any fmt verb.
- `internal/servecmd/serve.go`: `buildDeps` resolves env, then gh, runs the check, logs `github.connect` once, adds the gh token to the redactors only; start log carries the identity.
- `internal/configcmd/config.go`: offline `github auth:` line (app, token or none).
- `internal/merger/gates.go`: comment only. Decision: reviews are already COMMENT-only and gated on ard's records; under a PAT, the user's own approval does not count for protected paths, so a second user must approve.
- Docs: proposal Identity section, README, getting-started, gate-2 runbook, onboard-repo runbook.

## Testing
`go test ./...` passes; `go vet ./...` only prints a parse note for an existing testdata file. New tests: connect (token and App cases, each asserting no secret in the error), gh fallback with a fake `gh` on PATH, token formatting, `buildDeps` live token, failure before loops, gh states, gh-token leak and redaction, config check identity, and the merger PAT self-approval. Three config tests now ignore the new auth line.

## Open issues
- CLAUDE.md still says GitHub access is through an App installation token; it is protected, so a maintainer must update it.
- `ard init` still refuses a token outside dry-run (`internal/initcmd`); the plan did not cover it.
- Checks and statuses read cannot be probed for a fine-grained token without a commit, so only contents, issues and PRs are verified for tokens.
- A maintainer should confirm the self-review decision.
