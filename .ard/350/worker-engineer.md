---
role: engineer
status: done
attempt: 1
---
Adds `ard setup --create-gh-app`, which creates ard's GitHub App through GitHub's manifest flow: a built-in permission definition, a loopback one-shot callback server, an unauthenticated code conversion, a 0600 key file, printed env lines, and then the normal setup checks. This round fixes the build break found in review.

## Changes
- `internal/setupcmd/appmanifest.go`: built-in `AppPermissions` and `ForbiddenPermissions`, manifest builder and URL helper. The manifest-side comparison helper is renamed `ComparePermissions`, so it no longer clashes with the existing `CheckPermissions` in `cmd.go`.
- `internal/setupcmd/callback.go`, `convert.go`, `keyfile.go`, `createapp.go`: callback server, conversion with a redacted key, safe key write, and the create flow.
- `internal/setupcmd/cmd.go`: the permission check's manual hint now names `AppPermissions` and `ForbiddenPermissions`.
- `internal/cli`: the new flags and their conflict with `--check`.
- `docs/site/getting-started.md`: `--create-gh-app` is the first step, with the manual path as a fallback.
- Tests for each of the above.

## Testing
Ran `go vet ./internal/...` (clean) and `go test -race ./internal/setupcmd/ ./internal/cli/` (both pass). I did not run `make lint test`. The previous build failure came from the duplicate `CheckPermissions` name and is fixed by the rename. `TestManifestPermissionsMatchBuiltIn` and the other tests now use `ComparePermissions`.

## Review findings
- `appmanifest.go:239` (blocking): fixed. The helper is renamed `ComparePermissions` and the tests are updated.
- `cmd.go:110` (shared definition): partly fixed. The existing check still only tests that the repository is reachable, because the token's granted permissions are not available until #302. Its hint now references the shared definition. Full wiring waits for #302.
- `createapp.go:541` (dry-run GET): fixed by documenting it. Dry-run makes one read-only GET of the owner, to choose the user or organization URL, and creates nothing.

## Open issues
- `ComparePermissions` is not yet called by the real `ard setup` check (see above).
- `op` (1Password) writing is not covered by this change unless the earlier rounds added it.
