---
role: engineer
status: done
attempt: 1
---
An update-branch merge now keeps a review or approval covering the PR only when its tree equals git's clean mechanical merge of its parents, for every merge in the chain. Whitespace edits, conflict resolutions and hand-crafted merges therefore break the cover, and any failure (including a git without `merge-tree --write-tree`) fails closed.

## Changes
- `internal/gitx/inspect.go`: new `MergeTreeEqual` runs `git merge-tree --write-tree` on the two parents (attributes from the empty tree, no rerere, no renormalize) and compares the result with the merge's tree. Conflicts give false. Other errors, non-merges and unparsable output give an error.
- `internal/prgate/prgate.go`: `PatchGit` gains `MergeTreeEqual`; `firstUpdateMerge` checks every merge it walks and returns "" when one is not mechanical. Doc comments updated.
- `internal/merger/loop.go`: `Git` interface gains `MergeTreeEqual` (`*gitx.Git` satisfies it).
- `docs/proposal.md`: merger cover text corrected.
- Tests in `internal/gitx/inspect_test.go` and `internal/prgate/prgate_test.go`.

## Testing
Ran `go build ./...`, `go vet`, gofmt and `go test` for gitx, prgate, merger, reviewfix and reviewer: all pass. New tests: `TestMergeTreeEqual` (clean, whitespace, hand-resolved conflict, non-merge, bad rev, stub git rejecting merge-tree), `TestCoversWhitespaceMerge` (`.py`, `.yaml`, Makefile tab), `TestCoversNonMechanicalMerge`, `TestCoversChainSecondMergeNotMechanical`, a fake-based not-mechanical case and a merge-tree error case. Existing `TestCoversPatchEqualSharedFile` still covers the clean merge and two-merge chain.

## Open issues
- I did not add a separate merger-package approval test: the approval check calls `CoversDetail`, which the prgate real-repo tests exercise.
- The raw-diff hash was not adopted, as the plan decided. A clean merge by ard itself stays covered.
- I did not run `make lint` or `make test`; only the package tests listed above.
