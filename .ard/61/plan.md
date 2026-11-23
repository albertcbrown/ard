# Plan for #61

## Implementation

### Steps

1. **`internal/gitx/local.go` (new method)** `func (g *Git) ListTree(ctx context.Context, dir, rev, prefix string) ([]string, error)`: runs `ls-tree -r -z --name-only --full-tree <rev> -- <prefix>` with `--git-dir=gitDirOf(dir)` and `--literal-pathspecs`, same path validation as `FileAt` (reject empty, leading `/`, `..`). Returns repo-relative paths. Reason: nothing lists a directory today, and the committer takes file paths. Add a test next to the existing gitx tests using `gitx/gittest` fixtures.
2. **`internal/janitor/janitor.go` (new package)**, modelled on `internal/scanner/stale/stale.go`:
   - `const Name = "janitor"`, `Retention = 90 * 24 * time.Hour`, `Interval = 24 * time.Hour`.
   - Interfaces: `Reader` (`SearchIssues`, `GetIssue` from `github.Reader`), `Committer` (`Commit(ctx, committer.Request) (committer.Result, error)`), `Tree` (`Dirs(ctx) ([]int, error)` and `Files(ctx, issue int) ([]string, error)`, or one func type), and a `now func() time.Time`.
   - `type Janitor struct{...}`; `New(...)`; `Name`, `Interval(*config.Resolved)`, `Claims() bool` returns true (it writes main, so pause stops it), `Tick(ctx, controller.Env)`.
   - Monthly cadence: keep an in-memory `lastRun`; `Tick` returns nil unless 30 days have passed since the last completed pass (first tick after start always runs). The pass is idempotent, so losing the timestamp on restart only costs one extra scan.
   - Pass: list the `.ard/<n>/` directories on the fetched main (step 1; numeric names only, so `config.yaml`, `scanners/`, `triage/`, `workers/`, `reviewers/` are never candidates). Search `is:issue is:closed closed:<cutoff` with `SearchOpts{Max: 1000}` for candidates, and keep only issue numbers that have a directory. For each, re-fetch with `GetIssue` and require `State == closed`, `!IsPR`, `ClosedAt != nil` and `now - ClosedAt > Retention` (this guards reopened or racing issues). Skip any issue carrying `labels.Hold` or the `ard:keep` label, and log the skip.
   - Delete: for each eligible issue, build `committer.Request{Issue: n, Stage: "janitor", Role: "prune", Summary: "prune closed issue directory", Files: [{Path, Delete: true}...]}`, chunked to `committer.MaxFiles` per request. Commit only through the `Committer` interface; a guard error or conflict is logged and counted, and the loop moves to the next issue. Return a joined error at the end as `stale` does.
3. **`internal/servecmd/janitor.go` (new)** `func janitorLoop(w *wiring) controller.Loop`: wire `w.Reader`, `w.mainCommitter()` (the shared `lazyCommitter`; do not build a second committer) and a tree lister reading the controller clone at the main head that the committer lane fetches (check `internal/committer/refs.go` for the ref name; if none is readable outside the lane, add a small `committer.Committer` method that lists a prefix at the fetched head rather than re-fetching in the loop). Dry-run is honoured because the committer goes through `effects`.
4. **`internal/servecmd/serve.go`**: add `janitor.Name` to `knownLoops` (before `"reaper"`) and `loops = append(loops, janitorLoop(w))` in `registerLoops`. Update the `--loops` docs string in `internal/cli/cmd_serve.go` if it lists loop names. Check the existing loops-selection tests in `internal/servecmd` that assert the full list.
5. **Docs**: `docs/proposal.md` (the audit-trail paragraph near "Directories outlive their issues", the `--loops` list in the executor section, and the commit-message note) to say how the janitor runs: the 30-day cadence, the 90-day cutoff, the `ard:keep` and `ard:hold` skips, the commit as `ard(janitor:prune)`. Add `internal/janitor/` to the layout list in `CLAUDE.md`. Note: `CLAUDE.md` is a protected path, so that edit makes the PR wait for a human approval.

### Test plan

Command: `make test` (`go test ./...`), plus `make lint`. Tests use `github/fake` and a recording `Committer` fake.
- `TestJanitorPrunesOldClosed` (`internal/janitor/janitor_test.go`): issue closed 91 days ago with dir files `.ard/7/a.md`, `.ard/7/logs/x.log`. Expect one request with `Issue: 7` and a `Delete` entry per file.
- `TestJanitorKeepsRecent`: closed 89 days ago. Expect no commit.
- `TestJanitorSkipsOpenAndReopened`: open issue, and an issue the search returned but `GetIssue` shows reopened. Expect no commit.
- `TestJanitorNeverTouchesNonIssueDirs`: tree contains `.ard/config.yaml`, `.ard/triage/lead/...`, `.ard/workers/...`. Expect none in any request.
- `TestJanitorSkipsKeepAndHold`: labelled issues skipped.
- `TestJanitorChunksOver256Files`: 300 files give two requests, both `Issue: 7`.
- `TestJanitorContinuesAfterCommitError`: first issue's commit fails; the second is still pruned and `Tick` returns an error.
- `TestJanitorMonthlyGate`: a second `Tick` within 30 days does nothing; one after 30 days runs.
- `TestJanitorClaimsAndName`: `Claims()` is true; `Name()` is `janitor`.
- `TestListTree` (`internal/gitx`): nested files, a missing prefix (empty result), a bad path (error).
- servecmd: the unknown-loop and `registerLoops` tests include `janitor`.
Edge cases: an issue with no directory (nothing to do); a PR with the same number space; `ClosedAt` nil; a dir already deleted (committer treats absent delete paths as fine, so the commit is a NoOp).

### Risk

Medium. It deletes files from `main` through the committer, whose `CheckPath` already limits writes to `.ard/<issue#>/` for the request's own issue, so config and role directories cannot be hit even by a bug. Files touched are not in `merge.protected_paths` except `CLAUDE.md` (step 5, human approval needed). It adds a new background loop and changes `knownLoops` (public `--loops` interface). Race with a reopened issue is handled by the re-fetch. No migrations or concurrency beyond sharing the existing lazy main committer.

### Size

M (about 250 changed lines including tests). Files: `internal/gitx/local.go` (+test), `internal/janitor/janitor.go` and `janitor_test.go` (new), `internal/servecmd/janitor.go` (new), `internal/servecmd/serve.go` (+tests), `docs/proposal.md`, `CLAUDE.md`, possibly `internal/committer/` (listing helper).
