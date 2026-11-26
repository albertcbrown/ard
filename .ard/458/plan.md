# Plan for #458

## Implementation

### Steps
1. `internal/initcmd/initcmd.go`: add package seams `lookPath = exec.LookPath` and `runGh = github.ExecRun` (new vars, as in `internal/servecmd/serve.go` ~l.793) and a new `resolveAuth(ctx, e *cli.Env) (github.Auth, error)` that mirrors `servecmd.resolveAuth`: call `github.AuthFromEnv(os.Getenv, os.ReadFile)`; on `github.ErrNoCredentials` call `github.GhFallback(ctx, lookPath, runGh, e.Stderr)`; wrap errors as `ard init: %w` (the gh token stays in memory; nothing is written). Reason: same token sources as serve (#302). Use it in the `--pr` opener (replacing the `AuthFromEnv` call at ~l.48). Leave `localPermissions` (local mode, ~l.79) alone.
2. `initcmd.go`: delete `checkAuth` and its call (~l.52, l.98-104), so a token (env or gh) passes for live runs. `--dry-run` behaviour is unchanged. Keep the `e.DryRun` handling in `effects.Open`.
3. `internal/initcmd/body.go`: change `initBody(pl Result, res *config.Resolved)` to `initBody(pl Result, res *config.Resolved, kind github.AuthKind)`; update the call at `initcmd.go` ~l.248 (needs the kind in `deps`: add new field `AuthKind github.AuthKind`, set from `auth.Kind()` in the opener). Rename the final section to "## Follow-up ard does not do" and word it for both kinds: always say ard never writes `.github/`; for App: the App has no permission to write it; for token: the token may be able to write it, but ard still never does. Keep the three numbered steps, wording step 2 per kind (App: the App's `<slug>[bot]` login; token: the token user's login, with a note that the bot-commit audit cannot tell ard's main-lane commits from the user's). Keep the string `bot-commits.yml`, `ARD_BOT_LOGIN`.
4. `docs/runbooks/onboard-repo.md` (the #302 sentence at l.15-16 and the init step): state that `ard init --pr` works with an App, `ARD_GITHUB_TOKEN`, or the `gh` fallback. `docs/proposal.md` ~l.519 (init paragraph, `--pr` and permission check) and l.184 (the `ard init --pr` lane): say the same, and that the PR is still never written to `.github/`. Check `docs/site/getting-started.md` and `docs/layout.md` for "App only" wording and fix if present.
5. Tests (see below).

### Network inputs
None.

### Test plan
Test command: `make test` (or `go test ./internal/initcmd/...` for the targeted run; check the Makefile).
- `internal/initcmd/initcmd_test.go`: replace the `checkAuth` subtest with a table test of `resolveAuth` using temp env (`t.Setenv`) and fake `lookPath`/`runGh`:
  - App vars set -> `AuthApp`; `ARD_GITHUB_TOKEN` set -> `AuthToken`, gh not invoked; nothing set and fake gh logged in -> `AuthToken` from gh; nothing set and no gh -> error wrapping `ErrNoGh`; gh not logged in -> `ErrGhNotLoggedIn`; both App and token set -> error; errors never contain the token value.
- Live `--pr` run with a token-kind identity via the existing GitHub fake (`ghfake.WithIdentity(... Kind: github.AuthToken)`): opens the `ard-init` PR; the committed paths contain nothing under `.github/`. Same for App kind. Dry-run with token still logs only to the ledger.
- `initBody` table test (~l.515) for App and token: both contain "never"/`.github/` statement; the token body does not claim the App lacks permission; App body unchanged in substance.
- Edge: PR body contains no token text.

### Risk
Low to medium. Touches auth selection in a command that pushes a branch and opens a PR, but the committer lane (only `.ard/config.yaml`, lock and role trees, branch `ard-init`, lease on absent ref) is unchanged and still excludes `.github/`. No protected paths in `merge.protected_paths` are edited except none under `.ard/`. Public CLI behaviour changes (a live run no longer errors with a token). With a token, PRs are authored by the token's user, as documented for serve.

### Size
M (about 150-250 changed lines). Files: `internal/initcmd/initcmd.go`, `internal/initcmd/body.go`, `internal/initcmd/initcmd_test.go`, `docs/runbooks/onboard-repo.md`, `docs/proposal.md`, possibly `docs/site/getting-started.md`, `docs/layout.md`.
