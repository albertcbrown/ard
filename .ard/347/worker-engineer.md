---
role: engineer
status: done
attempt: 1
---
Adds read-only checks to `ard setup` for main's ruleset (requires a pull request, only the ard App bypasses, mode always), forbidden App permissions, and rebase merge when versioning is on. They are checked against a policy built into ard, and drift prints the exact fix.

## Changes
- `internal/github`: new `Ruleset`, `BypassActor`, `MergeSettings` and `AppInfo` types. `Reader` gains `ListRulesets`, `GetMergeSettings`, `GetApp` and `GetVariable`, implemented in `http.go` (`AppID` now delegates to `GetApp`).
- `internal/github/fake`: the same four methods, with seeders `SetRulesets`, `SetMergeSettings`, `SetApp` and `SetVariable`. `FailNext` works on them.
- `internal/setupcmd/policy.go` (new): the built-in policy constants and pure `checkRuleset`, `checkAppPerms` and `checkMerge` functions.
- `internal/setupcmd/setup.go`: optional `Deps.Policy` and `Deps.Repo`. New rows `ruleset`, `app-permissions` and `merge-settings` run before labels. A forbidden or not-found read gives a `manual` row with a `gh api` or settings-URL fix. Any non-green row skips the label apply step.
- `internal/setupcmd/cmd.go`: wires the new deps.

## Testing
- Ran `go test` on `./internal/setupcmd/... ./internal/github/... ./internal/merger/... ./internal/prwatch/...`: all pass. `go vet ./...` and gofmt are clean.
- Added `policy_test.go`: ok, each ruleset drift case, union across rulesets, forbidden gives manual, App permissions, merge and versioning, never writes, drift blocks apply.
- Added HTTP tests for the four new reader methods and a fake seeder test.
- `make lint test` timed out in this sandbox. The full `./internal/...` test run was not completed.

## Open issues
- The shared `clienttest` suite was not extended, since it targets a live API. The new methods are covered by httptest-based tests.
- Whether the App's own token can read ruleset bypass actors and repository variables needs a real-token check. A forbidden response already gives `manual`.
- The versioning switch is read from the `ARD_VERSIONING` repository Actions variable (on for 1, true, on or yes), as the plan states.
