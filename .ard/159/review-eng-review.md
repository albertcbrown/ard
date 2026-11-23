---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change follows plan.md: an unlabelled issue now yields no prefix, so the title is `<issue title> (#<n>)`. CodeMessage accepts an empty type, the truncation budget stays correct, and the tests and proposal are updated.

## Checks
- Lint: `go vet ./internal/committer/ ./internal/worker/` is clean.
- Tests: `go test ./internal/committer/ ./internal/worker/` passes.
- Diff coverage: not run as a profile. The new branches are exercised by the no-type, no-type-long, TestCommitType and unprefixed Title cases. The `bad type` case still rejects `Fix!`.
- Both call sites (run.go:532 and run.go:710) pass `CommitType` through unchanged. No non-test code matches on a `chore:` prefix.
