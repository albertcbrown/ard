---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/setupcmd/createapp.go"
    line: 594
    severity: medium
    blocking: true
    message: |
      If WriteKey fails after the conversion, the App already exists and the private key is lost.
      GitHub returns the key only once. With no --key-file, only the home directory is checked up front (`DefaultKeyPath("x")`), so an existing ~/.ard/keys/<slug>.pem, or an unwritable directory, fails after the App is created and the PEM is discarded.
      Fix: if the write fails, fall back to a fresh unique path (for example <slug>-<timestamp>.pem with O_EXCL) and print it. Alternatively, check the real default path as soon as the slug is known, which means before the conversion. The slug is predictable from the name, so a pre-check on `ard-<owner>-<repo>.pem` is possible. Add a test for it.
  - file: "internal/setupcmd/cmd.go"
    line: 62
    severity: medium
    message: |
      Plan step 2 says to wire the shared permission definition into the Permissions placeholder, or reference it from the real check. CheckPermissions and AppPermissions are used only by the manifest and tests, so setup's check does not share them yet.
      The issue's acceptance criterion says the definition is shared with `ard setup`'s permission check. The worker's report discloses that this is deferred to #302. Either wire it in or have a maintainer confirm the deferral.
  - file: "internal/setupcmd/createapp.go"
    line: 541
    severity: low
    message: |
      --dry-run makes a network GET (/users/{owner}) although plan.md's TestCreateAppDryRun says it makes no HTTP call. The worker's report discloses this and it is read-only. TestCreateAppDryRun does not assert on it. Consider an offline fallback in dry-run (assume a user, or accept a flag) or document the call.
  - file: "internal/setupcmd/keyfile.go"
    line: 1087
    severity: low
    message: |
      With --force, OpenFile with O_TRUNC follows a symlink at the key path, so the key is written to the symlink target. Consider Lstat and refusing symlinks, or removing and recreating the file. No test covers the unresolvable-home case with no --key-file either.
---
The change follows plan.md closely and its tests pass, but there is one real robustness bug. When the key write fails after the App is created, the one-time private key is lost. The shared permission check is also not wired in. This needs changes.

## Checks
- Lint: `make lint` was not run. The worker reports a pre-existing gofmt failure in a testdata file. I ran `go test -race -cover ./internal/setupcmd/ ./internal/cli/`, and both packages pass.
- Diff coverage: setupcmd coverage is 76.9% and cli is 97.7%. The tested paths are the callback, convert, key file, dry-run, user and org flows, the install timeout and the existing-key refusal. Not exercised: the real `openBrowser`, `lookupOwnerIsOrg` error paths, the unresolvable-home case and the WriteKey failure after conversion.
