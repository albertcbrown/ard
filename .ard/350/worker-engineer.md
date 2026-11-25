---
role: engineer
status: done
attempt: 1
---
Adds `ard setup --create-gh-app`, which creates ard's GitHub App through GitHub's manifest flow with a built-in permission set (no `workflows`, no `administration`), stores the key safely, and then runs the normal setup checks.

## Changes
- `internal/setupcmd/appmanifest.go`: the single `AppPermissions` / `ForbiddenPermissions` definition, `BuildManifest` (private, no webhook, refuses forbidden permissions), `ManifestURL` (user vs org) and `CheckPermissions`.
- `internal/setupcmd/callback.go`: one-shot 127.0.0.1 server with random port, constant-time `state` check, timeout and context cancel.
- `internal/setupcmd/convert.go`: unauthenticated code exchange. `Conversion` redacts the key in fmt and slog, and errors never include the response body.
- `internal/setupcmd/keyfile.go`: key written 0600 (dir 0700), refused if present unless `--force`. The path is checked before the browser step when `--key-file` is given.
- `internal/setupcmd/createapp.go`: the flow (owner lookup, dry-run, browser, convert, store, install link, poll for the installation, print and set the `ARD_GITHUB_*` lines).
- `internal/setupcmd/cmd.go`, `internal/cli/app.go`, `internal/cli/cmd_setup.go`: new flags `--create-gh-app`, `--app-name`, `--key-file`, `--force`. `--create-gh-app` with `--check` is a usage error, and the other three need `--create-gh-app`.
- `internal/github/auth.go`: exported `AppAuth.DiscoverInstallation` and `DefaultAPIBaseURL` (the architecture test allows the API host only in that package).
- `docs/site/getting-started.md`: the manifest flow is the first step, and the manual path is kept as the fallback.

## Testing
`go vet`, gofmt on the changed files, and `go test ./...` (setupcmd, cli and github also with `-race`) pass. The architecture test first failed on an `api.github.com` literal in setupcmd, so I moved the default into the github package. Added tests for permission parity, forbidden permissions, user/org URLs, callback loopback/state/single-use/timeout/cancel, conversion via httptest (including a body that echoes the key), key redaction, key file modes and refusal, dry-run, end-to-end create for user and org, install timeout, and flag parsing.

## Open issues
- The 1Password option is not implemented. The plan allowed deferring it, so it is left to a follow-up.
- The setup permission check is still the placeholder (#302). `CheckPermissions` is shared with the manifest and tested against it, but nothing yet reads the App's granted permissions to call it.
- `--dry-run` makes one read-only unauthenticated GET to look up the owner type (user vs org) so it can print the right URL. It creates nothing.
- `make lint` reports a pre-existing gofmt parse error in a testdata file (`internal/roles/testdata/...`), unrelated to this change. `make docs-check` cannot run here because `mkdocs` is not installed.
- A maintainer should review the callback server and key handling closely.
