---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/setupcmd/botlogin_test.go"
    line: 568
    severity: high
    blocking: true
    message: |
      plan.md's test plan names `TestBotLoginNeverFromConfig` (the value written is exactly the identity login whatever config says), and it still does not exist.
      The previous round rated this low, but a test plan case from plan.md that was not written is high under this role's rules. Add a test that puts a different bot login in the config or Desired and asserts the SetRepoVariable value is still the identity's login.
  - file: "internal/setupcmd/botlogin.go"
    line: 456
    severity: low
    message: |
      The printed fix `gh variable set ... --body ard-x[bot]` is unquoted. In zsh the `[bot]` glob makes the command fail with "no matches found".
      Quote the value, for example `--body 'ard-x[bot]'`, and update the expected string in botlogin_test.go.
---
The code is unchanged from the previous round and matches plan.md except for one case: the named test `TestBotLoginNeverFromConfig` is still missing. That is a high finding, so this needs changes.

## Checks
- `go test ./internal/setupcmd ./internal/effects ./internal/cli` passes.
- Lint: not re-run. The previous round's `make lint` reported nothing on the changed files.
- Diff coverage: no profile run. The tests exercise the added paths.

## Previous findings
- internal/setupcmd/botlogin.go (unquoted `--body` value): still present, at line 456.
- internal/setupcmd/botlogin_test.go (TestBotLoginNeverFromConfig missing): still present, now rated high.
