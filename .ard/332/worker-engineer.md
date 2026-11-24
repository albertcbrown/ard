---
role: engineer
status: done
attempt: 1
---
A path that both the PR and main changed now stays covered by an earlier review when the PR's own patch to it (stable patch id from the merge base) is unchanged across clean update-branch merges. This stops reviews going stale after every update-branch merge.

## Changes
- `internal/gitx/inspect.go`: `Parents` and `PatchID` helpers (fail closed).
- `internal/prgate/prgate.go`: `CoversDetail` with the patch-id rule, applied only when the head is reached from the reviewed commit through two-parent update-branch merges; `Covers` is a wrapper, so mark-ready and reviewfix landing are unchanged.
- `internal/merger/merge.go`: the review gate and approval check use `CoversDetail` and log `merge.cover_patch_equal`.
- `docs/proposal.md`: merger section describes the rule.
- Tests in prgate, gitx and merger.

## Testing
Ran `go test ./internal/merger`; passes. Review round: added `TestPatchEqualSharedFileKeepsReviewCovered` using a real repo, with three cases:
- Review gate: merges, no re-request, `merge.cover_patch_equal` logged.
- Hand-edited merge: re-requests, no patch-equal log.
- Protected-path approval check: stays approved, patch-equal log present.

## Review findings
- `internal/merger/merge.go:89`: fixed: merger tests added as listed above.
- `internal/prgate/prgate.go:140`: not changed: the non-blocking octopus/unrelated-first-parent test case was not added.
- `internal/prgate/prgate.go:252`: not changed: info note only.

## Open issues
- `make docs-check` was not run (mkdocs is not installed).
