# Plan for #332

## Implementation

### Steps
1. `internal/gitx/inspect.go` (new methods on `*Git`, same style as `MergeBase`, using `g.run` with `--git-dir=gitDirOf(repoDir)`):
   - `Parents(ctx, repoDir, rev string) ([]string, error)`: `git rev-list --parents -n 1 <rev>`, returns the parent SHAs.
   - `PatchID(ctx, repoDir, from, to, path string) (string, error)`: run `git diff --no-ext-diff --no-textconv --no-renames <from> <to> -- <path>` (use the `GIT_ATTR_SOURCE=emptyTree` and `GIT_OPTIONAL_LOCKS=0` env that `CodeChanged` uses), pipe it via the `stdin` argument of `g.run` into `git patch-id --stable`, and return the first field. An empty diff or empty output is an error (fail closed). Wrap errors like the neighbours do.
2. `internal/prgate/prgate.go`: widen the `Git` interface with `MergeBase`, `Parents` and `PatchID` (`*gitx.Git` already satisfies it). Update the fake in `prgate_test.go` and any other implementer of `prgate.Git` (grep `prgate.Git`, `CodeChanged(` in tests of `internal/merger` and `internal/reviewer`).
3. `internal/prgate/prgate.go`: add `CoversDetail(ctx, g, repoDir, reviewed, head, base string, issue int) (Cover, error)` where new `type Cover struct { OK bool; Broken, PatchEqual []string }`, and make `Covers` a thin wrapper returning `(c.OK, c.Broken, err)` so the reviewer's mark-ready caller (`internal/reviewer/ready.go:86`, `base ""`) is unchanged. In `CoversDetail`, after the existing loop computes `broken` (only when `base != ""`), if `broken` is non-empty run the new rule:
   - `chain(head, reviewed)`: walk first parents from `head`; every commit on the way must have exactly two parents; stop on reaching `reviewed`; cap at 100 steps. If `reviewed` is not reached, or any commit is not a two-parent merge, or any git error occurs, skip the rule (the plain result stands; for a git error return the error, not covered). Let `m1` be the merge whose first parent is `reviewed`.
   - `oldBase = MergeBase(reviewed, m1's second parent)`; `newBase = base`.
   - For each path in `broken`: `a = PatchID(oldBase, reviewed, p)`, `b = PatchID(newBase, head, p)`. Equal ids: the path goes to `PatchEqual`. Otherwise it stays in `Broken`. Any error returns `Cover{}` and the error.
   - `OK` when `Broken` is empty after the rule.
   Reason: a clean update-branch merge keeps the PR's own patch to a shared file; a conflict resolution, hand edit or later agent commit changes the patch id.
4. `internal/merger/merge.go:89` (review gate): call `prgate.CoversDetail`; when `PatchEqual` is non-empty log `t.log.Info("merge.cover_patch_equal", "pr", n, "issue", issue, "role", role, "reviewed", reviewed, "head", head, "paths", strings.Join(PatchEqual, ","))`. Keep `paths=len(Broken)` in `merge.reviews_stale`. Same at `:228` in `hasApproval` (log with the approval commit `at`); keep the "not comparable on error" behaviour there.
5. `docs/proposal.md`, merger section (line ~380, the head-rule sentence): replace "but a path both the PR and `main` changed since the review does" with the new rule: a path both sides changed is still covered when the PR's own patch to it (stable patch id of the diff from the merge base) is identical before and after, the head is reached from the reviewed commit only through update-branch merges, and any difference or git error is not covered; mention the `merge.cover_patch_equal` log and that mark-ready and reviewfix landing are unchanged. Run `make docs-check` if it checks the proposal.

### Test plan
Command: `make test` (`go test ./...`), plus `make lint`. Real-repo tests in `internal/prgate/prgate_test.go`, extending the helpers of `TestCoversRealRepo`; fake-based cases in `TestCovers`.
- `TestCoversPatchEqualSharedFile`: PR edits `docs/proposal.md` (hunk A) and is reviewed; main edits a different hunk of the same file; clean update-branch merge. Expect OK, `PatchEqual=[docs/proposal.md]`.
- `TestCoversConflictResolved`: the same file, but main edits the same lines and the merge is resolved by hand. Expect not OK, `Broken` holds the path.
- `TestCoversPrChangesAgain`: after the clean merge, a PR commit edits the file again. Expect not OK (head is not reached through merges only, and the patch differs).
- `TestCoversHandEditInMerge`: merge commit also changes the file beyond main's version. Expect not OK.
- `TestCoversChainOfMerges`: two successive clean update-branch merges. Expect OK.
- `TestCoversPatchGitError`: fake whose `PatchID` fails, and one whose `MergeBase` or `Parents` fails. Expect not OK and the error returned.
- `TestCoversBaseEmptyUnchanged`: `base ""` ignores the new rule.
- Non-merge head (plain descendant commit) and a merge with a non-reviewed first parent: rule skipped, same result as today.
- `internal/merger` tests: the review gate with a patch-equal path logs `merge.cover_patch_equal` and does not re-request; the approval check treats the same case as approved.
- `internal/gitx`: `PatchID` equal for the same change at shifted line offsets, differs for a changed line, errors on a bad rev.

### Risk
Medium. It loosens a security-relevant gate that also guards the protected-path approval check (`merge.protected_paths` in `.ard/config.yaml` such as `.github/`, `.ard/config.yaml`), so the rule must stay strict: two-parent merges only, first-parent chain to the reviewed commit, any error fails closed, `base ""` callers untouched. A merge commit can hide changes in non-conflicting files, but those paths are not in `broken` and are already tolerated today only when they match main's version. The change adds a public `prgate.Git` interface widening and a new log event. No migration or concurrency impact. No protected path is edited by the change.

### Size
M. Files: `internal/gitx/inspect.go`, `internal/prgate/prgate.go`, `internal/prgate/prgate_test.go`, `internal/merger/merge.go`, `internal/merger/*_test.go`, `internal/gitx` test file, `docs/proposal.md`.
