---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/setupcmd/cmd.go"
    line: 66
    severity: medium
    message: |
      The shared permission definition is still not wired into setup's Permissions row.
      AppPermissions and CheckPermissions are used only by the manifest and tests, although the issue says the definition is shared with `ard setup`'s check. The worker defers this to another issue. A maintainer should confirm the deferral. It does not block because the row is a placeholder owned by that other issue.
  - file: "internal/setupcmd/createapp.go"
    line: 541
    severity: low
    message: |
      --dry-run still makes a read-only GET /users/{owner} to choose the user or org URL, although plan.md says dry-run makes no HTTP call. Document the call or default offline in dry-run.
---
The manifest flow, the loopback callback, the key handling and the key-file fallback match plan.md and the issue, and tests cover them. Approved; two non-blocking findings carry over from the previous round.

## Checks
- Lint: `make lint` was not run, because the worker reports a pre-existing gofmt failure in an unrelated testdata file.
- Tests: `go test -cover ./internal/setupcmd/ ./internal/cli/` passes. setupcmd coverage is 79.6% and cli is 97.7%. Not exercised: the real `openBrowser`, the `lookupOwnerIsOrg` error paths and the unresolvable-home case.

## Previous findings
- internal/setupcmd/cmd.go (shared permission check not wired): still present, non-blocking.
- internal/setupcmd/createapp.go (dry-run makes a network call): still present, low.
