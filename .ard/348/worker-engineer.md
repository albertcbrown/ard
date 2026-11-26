---
role: engineer
status: done
attempt: 1
---
`ard setup` now sets the `ARD_BOT_LOGIN` repository variable from the App's own identity through a new `SetRepoVariable` effect that uses the operator's token, and checks the bot-commits workflow on `origin/main` against a copy embedded in ard. `ard setup --print-workflow` prints that copy; setup never commits.

## Changes
- `internal/github/http.go`: `(*HTTP).SetVariable` (update, create on 404). It is not part of `Writer`, so the App client cannot reach it, and go-github stays inside `internal/github` (arch test).
- `internal/effects`: `Effects.SetRepoVariable`; `Live` validates repo and name, reads the operator token only when called and never logs it; `ErrNoOperatorToken`; `Options.OperatorToken` and `Live.WithOperator`; `DryRun` only records to the ledger.
- `internal/setupcmd`: `botlogin.go` (check and apply; a different value is replaced only after `Confirm`, failures become manual rows with the `gh variable set` command), `workflow.go` and `workflow/bot-commits.yml` (embedded copy, `PrintWorkflow`, `checkWorkflow`), `setup.go` (new Deps, rows placed before the label sync and not blocking it), `cmd.go` (token func: PAT or gh token; terminal-only confirm prompt; `--print-workflow` handled first with only the workflow on stdout).
- `internal/cli`: `--print-workflow` flag, rejected with the other setup flags.
- Docs: `docs/proposal.md`, `docs/layout.md`, `docs/runbooks/onboard-repo.md`, `docs/site/getting-started.md`.

## Testing
`go vet ./...`, `gofmt -l` on changed files and `go test ./...` all pass. Added tests: `botlogin_test.go`, `workflow_test.go` (including embedded copy equals the repo file), `variable_test.go` (httptest: create on 404, update, bad input, no token, token not in logs, dry-run ledger), and a CLI flag test.

## Open issues
- The plan's `checkWorkflow(ctx, fileAt)` signature became `checkWorkflow(ctx, Deps.WorkflowAt)`; the file is read with `FileAt` and a 1 MiB cap.
- `cli.Env` has no stdin, so the confirm prompt reads `os.Stdin` only when it is a terminal and answers no otherwise; no `--yes` flag was added.
- The workflow and `ARD_BOT_LOGIN` rows do not block label sync, since their fix is manual or applied by their own step.
- `initcmd/body.go` was left unchanged.
