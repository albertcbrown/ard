---
role: engineer
status: done
attempt: 1
---
A path changed by both the PR and main now stays covered after an update-branch merge when the PR's own patch to that path is unchanged, so reviews no longer go stale on shared files such as the proposal. The rule applies to the merger's review gate and the approval check only.

## Changes
- `internal/gitx/inspect.go`: new `Parents` and `PatchID` (stable patch id of a one-path diff; an empty diff or git error is an error).
- `internal/prgate/prgate.go`: `CoversDetail` returns a `Cover` (OK, Broken, PatchEqual). It walks first parents from head to the reviewed commit through two-parent merges only (cap 100), takes the old merge base from the first merge's second parent, and compares patch ids per broken path. Any git error returns the error and is not covered. `Covers` keeps its signature and the plain rule, so mark-ready and reviewfix are unchanged.
- `internal/merger/merge.go`, `loop.go`: the review gate and approval check use `CoversDetail` and log `merge.cover_patch_equal`; the merger's `Git` interface gains `MergeBase`, `Parents` and `PatchID`.
- `docs/proposal.md`: merger section describes the rule.

## Testing
`go test ./...` passes. New real-repo tests cover a clean merge on a shared file (covered), a chain of merges, the PR changing the file again, a merge with a hand edit, and a merge standing in for a conflict resolution (all not covered). A fake-based test covers git errors and `base ""`. `gitx` tests cover `PatchID` and `Parents`.

## Open issues
- No merger-level test of the `merge.cover_patch_equal` log; the logic is covered in prgate.
- `make docs-check` could not run here (mkdocs is not installed).
- The old merge base comes from the first update merge's second parent, as the plan says, since main's tip at review time is not recorded.
