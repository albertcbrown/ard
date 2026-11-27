---
role: engineer
status: done
attempt: 1
---
`ard setup` now sets the `ARD_BOT_LOGIN` repository variable from the App identity (never from config) through a new operator-token-only `SetRepoVariable` on the effect layer, checks the bot-commits workflow on `origin/main` against a copy embedded in ard, and `ard setup --print-workflow` prints that copy. Setup never commits.

## Changes
- internal/effects: `SetRepoVariable` on `Effects`, Live and DryRun implementations, operator token plumbing and `ErrNoOperatorToken`.
- internal/setupcmd: `botlogin.go` (row and apply with confirm-before-overwrite), `workflow.go` plus embedded `workflow/bot-commits.yml`, new `Deps` fields, wiring in `cmd.go`.
- internal/cli: `--print-workflow` flag with rejected combinations.
- Docs: proposal, onboarding runbook, getting-started, layout.

## Testing
Ran `go test ./internal/setupcmd`: passes; gofmt clean on the changed files. Review round: added `TestBotLoginNeverFromConfig`.

## Review findings
- internal/setupcmd/botlogin_test.go: fixed: added `TestBotLoginNeverFromConfig`. It pre-sets a different login and puts a decoy login in Desired, then asserts the value written is the identity's login.
- internal/setupcmd/botlogin.go line 29: fixed: the printed `gh variable set` command now quotes the value (`--body 'ard-x[bot]'`); the test string is updated.

## Open issues
None.
