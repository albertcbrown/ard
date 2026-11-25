---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/setupcmd/cmd.go"
    line: 62
    severity: medium
    message: |
      The shared permission definition is still not wired into setup's Permissions row.
      AppPermissions and CheckPermissions are used only by the manifest and tests. The issue's acceptance criterion says the definition is shared with `ard setup`'s check. The worker's report defers this to #302/#347. A maintainer should confirm the deferral. It does not block, because the row is a placeholder owned by another issue.
  - file: "internal/setupcmd/createapp.go"
    line: 541
    severity: low
    message: |
      --dry-run still makes a read-only GET /users/{owner}, although plan.md says dry-run makes no HTTP call. The worker's report discloses this. Consider an offline default in dry-run, or document the call.
---
The previous blocking bug is fixed. A failed key write after conversion now falls back to a unique path, and a test covers it. The change follows plan.md, and the remaining findings are non-blocking. Approved.

## Checks
- Lint: `make lint` was not run. The worker reports a pre-existing gofmt failure in a testdata file. I ran `go test -cover ./internal/setupcmd/ ./internal/cli/`, and both packages pass.
- Diff coverage: setupcmd is 77.0% and cli is 97.7%. The new key fallback and symlink refusal are exercised. Not exercised: the real `openBrowser`, the `lookupOwnerIsOrg` error paths and the unresolvable-home case.

## Previous findings
- internal/setupcmd/createapp.go (key lost if write fails after conversion): resolved. `WriteKeyWithFallback` is in place and tested by `TestWriteKeyFallbackAndSymlink`.
- internal/setupcmd/cmd.go (shared permission check not wired): still present, reported again at its current line as non-blocking.
- internal/setupcmd/createapp.go (dry-run makes a network call): still present, low.
- internal/setupcmd/keyfile.go (--force follows a symlink): resolved. `WriteKey` refuses symlinks and a test covers it.
