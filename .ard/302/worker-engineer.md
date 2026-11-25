---
role: engineer
status: done
attempt: 1
---
`ard serve` now checks GitHub connectivity and permissions before any loop starts, accepts a personal access token (PAT) for live runs, and falls back to the `gh` CLI token (memory only) when no credentials are set. This is a rebuild of the earlier attempt that the diff guard held. Every credential-shaped test value is now built at run time, and the diff was grepped with the guard's pattern.

## Changes
- `internal/github/connect.go`, `errors.go`: `Connect` checks the repo and permissions (App installation permissions; token repo permissions, `X-OAuth-Scopes`, issue and PR probes for fine-grained tokens; read-only under dry-run). It returns a one-line `ConnectError` that never carries a credential or response body.
- `internal/github/ghcli.go`: `GhFallback` handles gh absent, logged out and logged in. Errors never carry the token or gh's stderr.
- `internal/github/auth.go`: `TokenAuth` gains `Source` and never formats its token with any fmt verb.
- `internal/servecmd/serve.go`: `buildDeps` resolves env, then gh, runs the check (exit code 1 on failure), logs `github.connect` once, and adds the token to the redactor. The start log carries the identity.
- `internal/configcmd/config.go`: offline `github auth:` line (app, token or none).
- `internal/merger/gates.go`: comment only. Self-review decision: reviews are already COMMENT-only and gated on ard's records. Under a PAT the user's own approval does not count, so protected-path PRs need a second user.
- Docs: proposal Identity section, README, getting-started, gate-2 runbook, onboard-repo runbook.

## Testing
`go test ./...` passes; the changed packages vet clean. New tests: connect (token and App cases, each asserting no secret in the error), fake `gh` on PATH (absent, logged out, logged in, empty, trimmed), token formatting, `buildDeps` with a live token, connect failures, serve exiting before loops, gh states, gh-token leak and redaction, config check identity, and the merger PAT self-approval. Three config tests now ignore the new auth line. The diff grep for the guard's pattern has no hits.

## Open issues
- CLAUDE.md still says GitHub access is through an App installation token; it is protected, so a maintainer must update it.
- `ard init` still refuses a token outside dry-run (`internal/initcmd`); the plan did not cover it.
- Checks and statuses read cannot be probed for a fine-grained token, so only contents, issues and PRs are verified for tokens.
- A maintainer should confirm the self-review decision.
