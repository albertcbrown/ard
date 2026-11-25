---
role: engineer
status: done
attempt: 1
---
Adds `ard setup`: it runs every check (origin/main comparison warning, permissions, `ard:*` labels), then applies label fixes. `--check` reports only and exits 2 on drift. `ard labels sync` stays as an alias.

## Changes
- `internal/setupcmd/`: report model and printer, `Run` (check phase, then apply), `Command` handler. The part after the client is opened is now `execute`, so tests can inject deps.
- `internal/cli/`: `SetupOpts`, `Handlers.Setup`, `cmd_setup.go`, registered in root; `labels sync` help text marks it an alias.
- `internal/labels/labelsync/cmd.go`: exported `ResolveRepo`.
- `cmd/ard/main.go`: wires `Setup`.
- `docs/layout.md`: lists `internal/setupcmd/`.
- Tests: setupcmd Run/Print/LocalVsMain cases, plus the new ones below.

## Testing
Ran `go test ./internal/setupcmd ./internal/cli ./cmd/...`; all pass. Test count is unchanged elsewhere.

## Review findings
- `internal/setupcmd/cmd.go:20` (blocking): fixed: split out `execute`; added `TestCheckExitsTwoOnDrift` (ExitError code 2, no writes, clean after apply) and `TestExecuteManualStepsError`.
- `internal/cli/cmd_setup.go:11`: fixed: added a handler case in `cli_test.go` (`setup --check` reaches `SetupOpts{Check:true}`, `labels sync` reaches `LabelsSync`) and `setup` in the help-list test.
- `internal/setupcmd/cmd.go:84`: not changed: `LocalVsMain` compares the worktree's `.ard/` to origin/main through `ChangedSince` with the same root. I did not verify that it covers uncommitted or untracked edits beyond the existing "differing" test, which uses an untracked file.

## Open issues
- `make lint` stops on a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file this change does not touch.
- The permission check is a placeholder until #302 lands.
