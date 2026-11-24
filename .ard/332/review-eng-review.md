---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/merger/merge.go"
    line: 89
    severity: high
    blocking: true
    message: |
      The merger wiring has no test, though plan.md's test plan lists two merger tests.
      Those cases are: the review gate with a patch-equal path logs `merge.cover_patch_equal` and does not re-request, and the approval check treats the same case as approved. No test was added under internal/merger, and the worker's report admits this. The review gate (consider) and hasApproval (line 229) are the security-relevant callers, and they are the only place the gate behaviour changes. Add merger tests that use a real repo (gittest) with a clean update-branch merge on a shared file. They should assert no `review_rerequest` and a merge. Add one case where the patch differs and the re-request still happens. Add one for hasApproval. Also assert that the log event appears.
  - file: "internal/prgate/prgate.go"
    line: 140
    severity: medium
    blocking: false
    message: |
      The "merge whose first parent is not the reviewed commit" skip and the maxChain cap are untested.
      plan.md named a "merge with a non-reviewed first parent" case. firstUpdateMerge is only exercised via a non-merge head and via the success paths. Add a case where a three-parent (octopus) or unrelated-first-parent head must leave the path broken.
  - file: "internal/prgate/prgate.go"
    line: 252
    severity: info
    message: |
      Soundness note: the old merge base comes from the first merge's second parent, so it is any commit, not necessarily main.
      The rule compares only the PR's own patch to paths both sides changed, and the plain rule still gates the other paths. That seems acceptable and matches the plan. Only the single-path patch is compared, so a rename-only change is not detected, but `--no-renames` keeps this fail-closed.
---
The core rule in prgate and gitx follows plan.md and fails closed on git errors, and it is well tested at the prgate level. The merger wiring, which is where the gate actually loosens, has none of the merger tests the plan asked for, so this needs changes.

## Checks
- Lint: `make lint` printed dependency downloads and one error in `internal/roles/testdata/docs/skips/src/_x.go`. That is a pre-existing testdata file, not touched by the change. Nothing was reported on the changed files.
- Diff coverage: `go test -cover ./internal/prgate ./internal/gitx ./internal/merger` passes. CoversDetail is at 100%, `cover` at 93%, firstUpdateMerge at 91%, and `Parents` and `PatchID` at about 85%. The new log lines in merge.go (`consider`, `hasApproval`) are not executed by any test.
- `make docs-check` was not run (mkdocs is not installed, per the worker).
