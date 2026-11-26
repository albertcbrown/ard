---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/setupcmd/botlogin.go"
    line: 28
    severity: low
    message: |
      The printed fix `gh variable set ... --body ard-x[bot]` is unquoted. In zsh the `[bot]` glob makes the command fail with "no matches found".
      Quote the value, for example `--body 'ard-x[bot]'`.
  - file: "internal/setupcmd/botlogin_test.go"
    severity: low
    message: |
      plan.md names TestBotLoginNeverFromConfig and an oversized-workflow edge case. Neither test is present. The code never reads config for the value, so the risk is small.
---
The change follows plan.md: SetRepoVariable is on the effect layer only, ARD_BOT_LOGIN comes from the App identity, a different value needs confirmation, `--print-workflow` is handled before any config or credentials, and setup never commits. Only two low findings, so approved.

## Checks
- `go build ./...` passes. The tests of internal/setupcmd, internal/effects, internal/cli and internal/github pass.
- `make lint`: the only output is a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file this change did not touch. Nothing was reported on the changed files.
- Diff coverage: no coverage profile run. I read the tests and they cover the main paths (absent, equal, different with and without confirmation, check-only, dry-run, not a bot, no token, forbidden read, workflow match and mismatch, and the print-workflow flag).
