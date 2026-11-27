---
role: engineer
status: done
attempt: 1
---
`ard init --pr` now accepts the same credentials as `ard serve` (GitHub App, `ARD_GITHUB_TOKEN`, or the `gh` CLI fallback held in memory) for live runs, and the PR body's follow-up section is worded for both auth kinds.

## Changes
- `internal/initcmd/initcmd.go`: new `resolveAuth` with `lookPath`/`runGh` seams, mirroring serve; removed `checkAuth`; `deps` carries `AuthKind`.
- `internal/initcmd/body.go`: `initBody` takes the auth kind; section renamed "Follow-up ard does not do"; says ard never writes `.github/`, with App and token wording for the `ARD_BOT_LOGIN` step.
- `internal/initcmd/initcmd_test.go`: dropped the `checkAuth` subtest; added `TestResolveAuth` (App, env token, gh fallback, no gh, gh not logged in, both set; no token in errors) and `TestInitBodyAuthKinds`.
- `docs/runbooks/onboard-repo.md`, `docs/proposal.md`: init works with either auth.

## Testing
`go vet ./internal/initcmd` and `go test ./internal/initcmd` pass. A run of `go test ./internal/...` hit my 2-minute tool timeout, so I did not see its full result.

## Open issues
- No new end-to-end live `--pr` test with a token-kind identity: the existing harness bypasses auth selection, and `resolveAuth` and the body wording are tested directly.
- `docs/site/getting-started.md` and `docs/layout.md` have no "App only" wording, so I left them unchanged.
