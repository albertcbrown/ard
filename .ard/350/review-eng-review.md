---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/setupcmd/cmd.go"
    line: 113
    severity: medium
    blocking: false
    message: |
      The shared permission check still does not compare granted permissions with `AppPermissions`; it only mentions them in an error string.
      The issue wants the manifest and `ard setup`'s check to share one definition. `ComparePermissions` has no caller outside tests. Not blocking because the full check belongs to #302, but wire `ComparePermissions` in when that lands.
  - file: "internal/setupcmd/cmd.go"
    line: 38
    severity: medium
    blocking: false
    message: |
      The new `o.CreateGHApp` branch in `Command` (dry-run return, then continuing into the normal checks) is not tested. `Command` coverage is 17.9%, and `CreateApp` and `DefaultKeyPath` are never executed.
      Add a test for the dry-run early return and for the env handoff to the checks.
  - file: "internal/setupcmd/createapp.go"
    line: 78
    severity: low
    blocking: false
    message: |
      The `--dry-run` GET /users/{owner} is now documented in the CreateApp comment, which resolves the earlier finding. plan.md said dry-run makes no HTTP call, so the docs could mention it too.
---
The compile error from the previous round is fixed (`ComparePermissions`), and the change implements the manifest flow with tests for the callback server, conversion, key handling and the user and org paths. Approved. The two medium findings do not block.

## Checks
- Lint: `make lint` could not complete here. It failed on a pre-existing parse error in `internal/roles/testdata/docs/skips/src/_x.go`, which this change does not touch. The packages build and the tests pass.
- Tests: `go test -race -cover` on setupcmd (80.1%), cli (97.7%) and github (88.4%) all pass.
- Diff coverage: `createApp` 77%, `Convert` 87%, `serveCallback` 92%, `WriteKey` 67%. Not executed by any test: `CreateApp`, `openBrowser`, `DefaultKeyPath`, and the `Command` branch for `--create-gh-app`.

## Previous findings
- internal/setupcmd/appmanifest.go (`CheckPermissions` redeclared): resolved. The helper is renamed `ComparePermissions` and the package compiles.
- internal/setupcmd/cmd.go (shared check not using the built-in definition): still present, medium. Only the error text mentions it.
- internal/setupcmd/createapp.go (dry-run network call): resolved. It is documented in the code comment.
