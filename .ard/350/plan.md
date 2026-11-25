# Plan for #350

## Implementation

### Steps
1. **New `internal/setupcmd/appmanifest.go`**: a single built-in permission definition `var AppPermissions = map[string]string{"contents":"write","issues":"write","pull_requests":"write","checks":"write","statuses":"read","metadata":"read"}` plus `var ForbiddenPermissions = []string{"workflows","administration"}`. Add `func BuildManifest(name string, redirectURL string) Manifest` (new type, JSON tags per GitHub manifest: `name`, `url`, `redirect_url`, `public:false`, `default_permissions`, `default_events: []`, no `hook_attributes`) and `func ManifestURL(owner string, ownerIsOrg bool, state string) string` (`https://github.com/settings/apps/new?state=` for a user, `https://github.com/organizations/<org>/settings/apps/new?state=` for an org). `BuildManifest` must panic or error if a forbidden permission is present. Reason: one definition shared by the manifest and the check.
2. **Same file, `func CheckPermissions(granted map[string]string) (missing, extra []string)`**, built on `AppPermissions` and `ForbiddenPermissions`. Wire it into the existing `Permissions` placeholder in `internal/setupcmd/cmd.go` only if #302 has not landed. Otherwise just reference the shared definition from the real check. Reason: the issue requires one definition shared with the check.
3. **New `internal/setupcmd/callback.go`**: `func serveCallback(ctx, manifestJSON []byte, state string, timeout time.Duration, ready func(url string)) (code string, err error)`. It listens with `net.Listen("tcp","127.0.0.1:0")`, uses `crypto/rand` for the state, and serves `GET /` as an auto-submitting HTML form (manifest HTML-escaped, POSTed to the manifest URL). `GET /callback` compares `state` with `subtle.ConstantTimeCompare`, accepts exactly one valid request, then calls `Shutdown`. It also shuts down on timeout or context cancel (default 5m). Wrong state returns 400 and the server keeps waiting, but only one valid request is ever accepted. Reason: the loopback-only, one-shot server from the acceptance criteria.
4. **New `internal/setupcmd/convert.go`**: `type Conversion struct{ ID int64; Slug string; PEM string; ... }` and `func Convert(ctx, apiBase string, hc *http.Client, code string) (Conversion, error)`, which does `POST {apiBase}/app-manifests/{code}/conversions` unauthenticated. `PEM` carries `json:"-"`-style handling and a `String()`/`GoString()`/`LogValue()` that redact it. Error text is built only from the status and GitHub's `message`, never from the body. Reason: the key must not reach logs or errors. `apiBase` is a parameter so the test can pass an httptest server.
5. **New `internal/setupcmd/keyfile.go`**: `func WriteKey(path string, pem []byte, force bool) error`. It creates the parent directory with `0700`, opens with `O_WRONLY|O_CREATE|O_EXCL` and mode `0600`, and returns an error when the path exists unless `force` (then it truncates and `chmod 0600`). Default path `~/.ard/keys/<slug>.pem` via `os.UserHomeDir`. Reason: safe key storage. The pre-existing-path check should run before the browser step, so the user does not create an App that cannot be stored.
6. **New `internal/setupcmd/createapp.go`**: `func CreateApp(ctx, e *cli.Env, repo github.Repo, o CreateAppOpts) error`. The flow is:
   - Resolve the owner type with `GET /users/{owner}` (type `Organization` or `User`).
   - Build the manifest and state.
   - On `e.DryRun`, print the manifest JSON and the URL, create nothing, and return.
   - Check the key path (`--force` rules).
   - Start the server, print the local URL (open the browser through an injectable `openBrowser func(string) error` that uses `open` or `xdg-open` and falls back to printing the URL).
   - Convert the code and write the key (`0600`).
   - Print the install link `https://github.com/apps/<slug>/installations/new`.
   - Poll for the installation by building `github.AppAuth` from the new ID and key and calling its existing installation discovery (`internal/github/auth.go`, `discover`; export a small wrapper if needed). Poll every 3s up to a timeout, and on timeout print the rerun instructions.
   - Print `ARD_GITHUB_APP_ID`, `ARD_GITHUB_INSTALLATION_ID` and `ARD_GITHUB_PRIVATE_KEY_FILE` lines (never the key).
   - Set the env vars in-process and continue into the normal setup flow.
   - `CreateAppOpts` is `{Name, KeyPath string; Force bool; OnePassword bool}`. The 1Password write is optional: when `--op-item` is set and `op` is on PATH, run `op item create` through `exec.CommandContext` with the key passed as a file reference. If it is too large, defer this to a follow-up and say so in the PR.
7. **`internal/cli/app.go` and `internal/cli/cmd_setup.go`**: extend `SetupOpts` with `CreateGHApp bool; AppName, KeyFile string; Force bool`. Add flags `--create-gh-app`, `--app-name`, `--key-file` and `--force`. `--create-gh-app` conflicts with `--check` (return a usage error). Update `internal/cli/cli_test.go` for the new fields.
8. **`internal/setupcmd/cmd.go`**: at the top of `Command`, after repo resolution and before `AuthFromEnv` (which would fail with no credentials), branch on `o.CreateGHApp` to call `CreateApp`, then continue with the normal path using the new env.
9. **Redaction**: pass every output through the existing `internal/redact` helpers where practical. Ensure `Conversion` is never given to `slog`.
10. **Docs**: edit `docs/site/getting-started.md` section 1 so that `ard setup --create-gh-app` (with `--dry-run` to preview) is the first step, and keep the manual path as "Fallback: create the App by hand". Update the `#350` mention in `docs/proposal.md` only if the behaviour differs. Run `make docs-check`.

### Test plan
Test command: `make lint test` (the worker's configured test command). Run `go test ./internal/setupcmd/... ./internal/cli/...` while iterating.
- `TestManifestPermissionsMatchBuiltIn` (`appmanifest_test.go`): the manifest `default_permissions` equal `AppPermissions` and the `CheckPermissions` expectations. `workflows` and `administration` are absent, and `public` is false with no webhook.
- `TestManifestRejectsForbidden`: adding `workflows` to the definition makes `BuildManifest` fail.
- `TestManifestURLUserVsOrg`: the user URL is `github.com/settings/apps/new`, the org URL is `/organizations/<org>/settings/apps/new`, and both carry `state`.
- `TestCallbackBindsLoopbackOnly`: the listener address is `127.0.0.1`.
- `TestCallbackStateMismatch`: a wrong state returns 400 and no code. A later correct request succeeds.
- `TestCallbackSingleUse`: after one valid request the server is closed, and a second request fails to connect.
- `TestCallbackTimeout` and `TestCallbackContextCancel`: a short timeout returns an error and the listener is closed.
- `TestConvertHTTPTest` (`convert_test.go`): an httptest fake for `/app-manifests/{code}/conversions` returns id, slug and pem. Errors on 404 and 422 carry no body text.
- `TestKeyNeverLeaks`: the PEM is not found in the output of `fmt.Sprintf("%v %+v %#v")` of `Conversion`, in a captured `slog` buffer, in stdout/stderr of `CreateApp`, or in any error string, including a failing conversion whose fake body echoes the key.
- `TestWriteKeyMode0600`: the file mode is `0600` and the directory is `0700`.
- `TestWriteKeyRefusesExisting`: an existing path errors without `force` and the contents are unchanged. With `force` it is overwritten and the mode is `0600`.
- `TestCreateAppDryRun`: with `DryRun` it prints the manifest and the URL, makes no HTTP call, starts no listener and writes no file.
- `TestCreateAppUserAndOrg`: end-to-end with fakes for the owner lookup, the browser opener (which hits `/callback` itself) and the conversion, for both owner types, checking the printed env lines.
- `TestSetupFlagsParse` (`internal/cli`): the new flags populate `SetupOpts`, and `--create-gh-app --check` is rejected.
- Edge cases: the install poll times out (rerun message, exit non-zero), the browser opener fails (URL is printed), the home dir is unresolvable and no `--key-file` is given (clear error).

### Risk
Medium. It adds a local HTTP listener and handles a private key, both security-sensitive, but it touches no protected paths from `merge.protected_paths` (`docs/` and `internal/` are not listed) and does not edit `.github/`. The public CLI interface gains flags (additive). There are no migrations. The one concurrency point is the server goroutine's shutdown, which tests must cover with `-race`. The 1Password step shells out to `op`, so pass the key by a temporary-file or stdin argument rather than argv.

### Size
L, roughly 900 to 1,300 changed lines including tests. Files touched: `internal/setupcmd/appmanifest.go`, `callback.go`, `convert.go`, `keyfile.go` and `createapp.go` (all new), their `_test.go` files (new), `internal/setupcmd/cmd.go`, `internal/cli/app.go`, `internal/cli/cmd_setup.go`, `internal/cli/cli_test.go`, `internal/github/auth.go` (a small export, if needed) and `docs/site/getting-started.md`.
