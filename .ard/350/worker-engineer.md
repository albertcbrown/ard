---
role: engineer
status: done
attempt: 1
---
Adds `ard setup --create-gh-app`, which creates ard's GitHub App through GitHub's manifest flow: a built-in least-privilege manifest, a loopback one-shot callback server, an unauthenticated code conversion, a 0600 key file, an install wait and the environment lines. This round hardens key storage after review.

## Changes
- internal/setupcmd (appmanifest, callback, convert, keyfile, createapp): the flow as planned. Permissions come from one built-in definition (workflows and administration are never requested).
- keyfile.go: new `WriteKeyWithFallback`. If the key write fails after the App exists, the key goes to a fresh `<name>-<timestamp>.pem` (O_EXCL, 0600) beside the target, then under ~/.ard/keys, then in the temp dir, and the path is printed. `WriteKey` now refuses a symlink at the key path, even with `--force`.
- createapp.go: uses the fallback after conversion. An error says the App was already created.
- internal/cli: the `--create-gh-app`, `--app-name`, `--key-file` and `--force` flags; the combination with `--check` is rejected.
- docs/site/getting-started.md: `--create-gh-app` is the first step, with the manual path as fallback.

## Testing
Ran `go test -race ./internal/setupcmd/ ./internal/cli/`; both pass. Added `TestWriteKeyFallbackAndSymlink` (fallback when the target exists, original untouched, symlink refused).

## Review findings
- createapp.go key write after conversion: fixed. Fallback path added and tested.
- cmd.go:62 shared permission check not wired: not changed. The setup Permissions row is a placeholder owned by #302/#347. `CheckPermissions` and `AppPermissions` are ready for it. A maintainer should confirm the deferral.
- createapp.go dry-run owner lookup: not changed. It is a read-only GET to find user vs org so the printed URL is right. It could be made offline with a flag later.
- keyfile.go symlink with --force: fixed. Symlinks are refused. The unresolvable-home case still has no test.

## Open issues
- The two items above that were not changed. `make lint` was not run, since an earlier gofmt failure in a testdata file was reported as pre-existing.
