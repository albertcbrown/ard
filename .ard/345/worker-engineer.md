---
role: engineer
status: done
attempt: 1
---
`ard init` now writes the starter `.ard/` into the local checkout by default, and `--pr` keeps the `ard-init` branch and PR lane. Label sync is gone from init (setup owns it), and init runs the permission check and reports it: a failure warns in local mode and fails in `--pr` mode.

## Changes
- `internal/initcmd/local.go` (new): local mode. It refuses when `.ard/` has uncommitted or untracked changes, and plans with the existing lock-based planner against the working tree. It validates the merged config before writing, then writes atomically with no symlinked components and nothing outside `.ard/`, and never deletes. `--dry-run` prints the plan only. It ends with the next steps.
- `internal/initcmd/initcmd.go`: `Init` branches on `PR`. The `--pr` lane loses label sync, fails on a non-ok permission row before any push, and prints the next step.
- `internal/setupcmd/cmd.go`: the permission placeholder is now the exported `CheckPermissions`, shared by setup and init (#302 can replace its body).
- `internal/gitx/gitx.go`: `DirtyPaths` (git status under a pathspec).
- `internal/cli`: `--pr` flag, `InitOpts.PR`, new short help.
- `docs/proposal.md`: removed the sentence saying init behaves as before until #344 and #345 land.
- Tests: new `local_test.go` (write tree, second run no-op, dirty refusal, dry run, never deletes edited files, symlink escape, permission warning, invalid config). Existing init tests now assert no labels are created, and the label-failure test is now `TestInitPRPermissionFailureStopsBeforePush`. Added a cli `--pr` test and a `CheckPermissions` test.

## Testing
`go vet ./...` and `go test ./internal/...` pass. An early run failed because the exec bit was lost when reading the local tree, so a second run wanted to update scripts. It now reads the full file mode.

## Open issues
- `TestLocalInvalidConfigWritesNothing` skips itself when the seeded bad config is accepted as an edited file; it is weak coverage of that path.
- `body.go` had no label wording, so it is unchanged.
- Local mode's permission check opens a dry-run client for reads only; with no repo or credentials it prints a warning and continues.
