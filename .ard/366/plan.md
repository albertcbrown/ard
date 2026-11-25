# Plan for #366

## Implementation

### Steps
1. `internal/gitx/inspect.go` (new): `func (g *Git) MergeTreeEqual(ctx, repoDir, merge string) (bool, error)`. Read the parents with `Parents` (exactly two, else error). Run `git --git-dir=… -c rerere.enabled=false -c merge.renormalize=false merge-tree --write-tree <p1> <p2>` with env `GIT_ATTR_SOURCE=emptyTree`, `GIT_OPTIONAL_LOCKS=0`. Exit 1 (conflicts) returns `false, nil` (use `isExit(err, 1)`). Any other error (including an old git without `--write-tree`) returns an error. Take the first output line as the tree id and compare it to `rev-parse <merge>^{tree}`. Empty or unparsable output is an error.
2. `internal/prgate/prgate.go`: add `MergeTreeEqual(ctx, repoDir, merge string) (bool, error)` to `PatchGit`. Change `firstUpdateMerge` to call it on every merge it walks (each `cur` with two parents) and return `""` when a merge is not mechanical. Return an error on failure, which `cover` already turns into not covered. Update the doc comments of `CoversDetail` and `firstUpdateMerge`.
3. `internal/merger/loop.go`: add `MergeTreeEqual` to the `Git` interface (`*gitx.Git` satisfies it). Update fakes that implement `prgate.PatchGit` or `merger.Git`: `internal/prgate/prgate_test.go` (`errPatchGit` and the fake it wraps) and the merger and reviewer test fakes (grep `PatchID(` in `*_test.go`).
4. `docs/proposal.md` (the merger review-gate paragraph near the patch-id rule): replace "A conflict resolution, a hand edit or a later PR commit changes the patch and breaks the cover" with text saying every merge in the chain must equal git's clean merge of its parents (so conflict resolutions, whitespace edits and hand-crafted merges break the cover), a later PR commit breaks it, and a missing `merge-tree --write-tree` fails closed.

### Test plan
Command: `make test` (also `make lint`). Real-repo tests use the `patchRepo` helper in `internal/prgate/prgate_test.go`.
- `TestCoversWhitespaceMerge` (prgate): PR and main both edit `doc.md`; merge with `-s ours`-style manual tree whose PR lines are re-indented (`.py` and `.yaml` files, and a tab in a `Makefile`). Expect not OK, `Broken` holds the path.
- `TestCoversNonMechanicalMerge`: hand-crafted merge via `commit-tree` with a tree that differs from the clean merge. Expect not covered.
- `TestCoversPatchEqualSharedFile` (existing): a clean merge touching different hunks stays covered, including the two-merge chain; add a chain whose second merge is non-mechanical and expect not covered.
- `TestCoversMergeTreeError`: fake whose `MergeTreeEqual` errors. Expect not OK and the error returned.
- `internal/gitx/inspect_test.go` `TestMergeTreeEqual`: clean merge gives true; conflict merge resolved by hand gives false; whitespace-altered merge gives false; a non-merge commit or bad rev gives an error. Fail-closed case: a git binary stub that rejects `merge-tree --write-tree` gives an error.
- Merger approval test in `internal/merger`: a stale human protected-path approval with a whitespace-altered merge is not honoured.

### Risk
Medium. This tightens, not loosens, a security-relevant gate that guards the human protected-path approval (`internal/merger/merge.go` approval check and review gate). `merge.protected_paths` is untouched. Two interfaces widen, `prgate.PatchGit` and `merger.Git`. There are no migrations or concurrency changes. `merge-tree` writes objects into the shared clone but changes no refs.

### Size
M. Files: `internal/gitx/inspect.go`, `internal/gitx/inspect_test.go`, `internal/prgate/prgate.go`, `internal/prgate/prgate_test.go`, `internal/merger/loop.go`, merger test fakes, `docs/proposal.md`.
