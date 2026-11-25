# Plan for #347

## Implementation

### Steps
1. `internal/github/types.go` (new types): `Ruleset{ID int64; Name, Target, Enforcement string; Branches []string /*ref include patterns*/; Rules []string /*rule types, e.g. pull_request*/; Bypass []BypassActor}`, `BypassActor{ActorType string; ActorID int64; Mode string /*always|pull_request*/}`, `MergeSettings{AllowRebaseMerge, AllowSquashMerge, AllowMergeCommit bool}`, `AppInfo{ID int64; Slug string; Permissions map[string]string}`. Reason: a domain model that does not leak go-github types, matching the existing style (`RequiredChecksResult`).
2. `internal/github/client.go`: add to `Reader` (documented, all read-only):
   - `ListRulesets(ctx) ([]Ruleset, error)` - active branch rulesets of the repo with bypass actors; ErrForbidden when the token cannot read bypass actors (admin read).
   - `GetMergeSettings(ctx) (MergeSettings, error)` - `GET /repos/{o}/{r}`; `allow_rebase_merge` is visible to any reader.
   - `GetApp(ctx, slug string) (AppInfo, error)` - `GET /apps/{slug}` (same call as `AppID`, which can delegate to it); public data includes `permissions`.
   - `GetVariable(ctx, name string) (value string, found bool, err error)` - `GET /repos/{o}/{r}/actions/variables/{name}`, 404 gives found=false, 403 gives ErrForbidden. Needed to know whether `ARD_VERSIONING` is on.
3. `internal/github/http.go`: implement the four methods with go-github (`Repositories.GetAllRulesets` then `GetRuleset` per id for bypass actors and conditions, `Repositories.Get`, `Apps.Get`, `Actions.GetRepoVariable`), wrapping errors with `h.fail(...)` like `AppID`. Keep only `enforcement == "active"` and `target == "branch"` in `ListRulesets`. Map errors via the existing `ErrForbidden`/`ErrNotFound`.
4. `internal/github/fake/fake.go`: add state fields and seeders `SetRulesets(...github.Ruleset)`, `SetMergeSettings(github.MergeSettings)`, `SetApp(slug string, github.AppInfo)`, `SetVariable(name, value string)`; implement the four methods with `beginRead(...)` so `FailNext` works (`FailNext("ListRulesets", github.ErrForbidden)` gives the manual case). Fake default: empty rulesets, rebase allowed, no variables.
5. `internal/github/clienttest/suite.go` and `internal/github/http_test.go`: extend the shared suite with the new methods (see test plan). Existing tests must still pass; any other struct implementing `github.Reader` outside the fake would not compile, so grep for them (none found beyond narrow consumer interfaces).
6. New `internal/setupcmd/policy.go`: built-in policy, constants only, never read from repo content: `const (policyBranch = "main"; policyBypassMode = "always"; forbiddenAppPerms = {"workflows","administration"})`. Pure functions:
   - `checkRuleset(rs []github.Ruleset, app github.AppInfo) Row`: pick active rulesets that cover `refs/heads/main` (or `~DEFAULT_BRANCH`); drift when none has a `pull_request` rule, or when the union of bypass actors is not exactly the ard App (`ActorType=="Integration"`, `ActorID==app.ID`, mode `always`) - extra actors, wrong mode or a missing App are each named in Detail.
   - `checkAppPerms(app github.AppInfo) Row`: drift if `workflows` or `administration` is present.
   - `checkMerge(ms github.MergeSettings, versioning bool) Row`: drift when versioning is on and `AllowRebaseMerge` is true. When off, ok.
   Each drift Detail carries the exact fix: the settings URL `https://github.com/{owner}/{repo}/settings/rules` or `.../settings/installations`, or a `gh api -X PATCH repos/{owner}/{repo} -F allow_rebase_merge=false` command. The fix is only printed, never run.
7. `internal/setupcmd/setup.go`: add `Deps.Policy PolicyReader` (narrow interface declared here with the four methods plus `Repo github.Repo`, `AppSlug` derived from `Reader.Identity` login minus `[bot]`). In `Run`, after the permissions row is ok and before the labels row, append rows `ruleset`, `app-permissions`, `merge-settings`. Any `github.ErrForbidden` from a read gives `StatusManual` with the exact fix (URL or `gh api` read command), never a hard error; other errors return `setup: ...`. These rows are checks only: they never reach the apply step, but a drift row must keep the existing rule that apply happens only when green (`Run` already returns early before labels are applied when not green: verify and adjust so the labels apply branch is skipped when any new row is drift/manual; only `rep.Green()` gates it). Add `const rulesetName, appPermName, mergeName`.
8. `internal/setupcmd/cmd.go`: wire the new Deps in `Command` using `rd` (the Reader from `effects.Open`) and `repo`.
9. `docs/proposal.md` (line about `ard setup`) already describes these checks; no change needed. Mention nothing else.

### Test plan
Command: `make lint test` (worker test command in `.ard/config.yaml`); targeted `go test ./internal/github/... ./internal/setupcmd/...`.
- `internal/setupcmd/setup_test.go`, using `fake.Fake`:
  - `TestRulesetOK`: ruleset with `pull_request` rule, bypass exactly App/always -> row ok.
  - `TestRulesetNoPR`: no pull_request rule -> drift, Detail has the settings URL.
  - `TestRulesetExtraBypass`: extra actor (e.g. a team) -> drift naming it; `TestRulesetBypassPullRequestMode`: mode not always -> drift; `TestRulesetAppMissing`: no App bypass -> drift.
  - `TestRulesetInactiveIgnored`: disabled/evaluate ruleset does not satisfy the policy; no ruleset at all -> drift.
  - `TestRulesetForbiddenIsManual`: `FailNext("ListRulesets", ErrForbidden)` -> manual with a `gh api` command, no error, exit path "manual steps needed".
  - `TestAppPermsWorkflows` / `...Administration` -> drift; clean permissions -> ok.
  - `TestMergeRebaseVersioning`: versioning "on" + rebase allowed -> drift with the `gh api -X PATCH` fix; versioning off or unset -> ok; `GetVariable` forbidden -> manual.
  - `TestChecksNeverWrite`: `f.Writes()` empty after `--check` and after drift (only labels may be written when green).
  - `TestDriftBlocksApply`: a drift in the new rows means no labels are created.
  - `TestPrintManualFix`: `Print` shows the fix text.
- `internal/github/clienttest/suite.go` / `http_test.go` (httptest server, existing helpers): `ListRulesets` returns only active branch rulesets with bypass actors; 403 maps to ErrForbidden; `GetMergeSettings` parses `allow_rebase_merge`; `GetApp` parses permissions; `GetVariable` 404 -> found=false.
- `internal/github/fake/fake_test.go`: seeders round-trip and FailNext.
- Edge cases: empty bypass list, multiple rulesets whose bypass lists union, `~DEFAULT_BRANCH` include pattern, pagination of rulesets.

### Risk
Medium. Adds public `github.Reader` methods, so every implementer must compile (fake, HTTP, clienttest). Security-sensitive in meaning (checks branch protection) but strictly read-only; no write method is added, and the policy is built in, not from repo or config. No protected paths from `merge.protected_paths` are touched (`docs/proposal.md` untouched). Admin-scoped reads may be forbidden for App tokens, so the manual fallback matters; GitHub's ruleset bypass actor visibility needs checking against a real token (not testable here). No migrations or concurrency concerns.

### Size
L at about 500-700 changed lines including tests. Files: `internal/github/types.go`, `client.go`, `http.go`, `http_test.go`, `clienttest/suite.go`, `fake/fake.go`, `fake/fake_test.go`, `internal/setupcmd/policy.go` (new), `policy_test.go` (new), `setup.go`, `setup_test.go`, `cmd.go`.
