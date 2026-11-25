---
role: eng-review
status: done
attempt: 2
verdict: changes
findings:
  - file: "internal/setupcmd/appmanifest.go"
    line: 239
    severity: critical
    blocking: true
    message: |
      The package does not compile: `CheckPermissions` is declared twice in package setupcmd.
      internal/setupcmd/cmd.go:110 already has `CheckPermissions(ctx, r labelsync.Reader, repo github.Repo) Row`, used at cmd.go:64 and in cmd_test.go:99. This new `CheckPermissions(granted map[string]string)` clashes with it (`go vet`: "CheckPermissions redeclared in this block"), so `go test ./internal/setupcmd/` fails to build.
      Rename the new helper (for example `ComparePermissions`) and update TestManifestPermissionsMatchBuiltIn. Better, have the real check at cmd.go:110 use `AppPermissions` and `ForbiddenPermissions`, which is what the issue asks for. Also rebase on the base branch and run `make lint test`.
  - file: "internal/setupcmd/cmd.go"
    line: 110
    severity: medium
    blocking: false
    message: |
      The existing shared permission check does not use the new built-in `AppPermissions` definition, so the issue's "single definition shared with `ard setup`'s check" is not met.
      Make the real check read `AppPermissions` and `ForbiddenPermissions`, and keep the test that compares the manifest with it.
  - file: "internal/setupcmd/createapp.go"
    line: 541
    severity: low
    message: |
      --dry-run still makes a read-only GET /users/{owner}, although plan.md says dry-run makes no HTTP call. Document it or default offline in dry-run.
---
The change does not build: `CheckPermissions` is declared in both appmanifest.go and cmd.go, so the setupcmd tests cannot run. This needs changes.

## Checks
- Lint: `go vet ./internal/setupcmd/ ./internal/cli/ ./internal/github/` fails with "CheckPermissions redeclared". `gofmt -l` on the changed packages is clean. `make lint` was not run.
- Tests: `go test -race -cover ./internal/setupcmd/ ./internal/cli/`: setupcmd build failed; cli passes (97.7%). Diff coverage for setupcmd could not be measured.

## Previous findings
- internal/setupcmd/cmd.go (shared permission check not wired): still present, now medium. The base branch already has a real check there, which makes the redeclaration worse.
- internal/setupcmd/createapp.go (dry-run network call): still present, low.
