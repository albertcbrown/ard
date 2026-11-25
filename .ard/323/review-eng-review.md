---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/worker/conflict.go"
    line: 42
    severity: high
    blocking: true
    message: |
      The worker side of the conflict round has no test of its main path: conflictInput, preMerge and the MergeParent push in run.go.
      plan.md's test plan lists TestConflictRoundInput, TestConflictRoundCleanMerge and TestConflictRoundResolved, and none exist.
      conflict_test.go covers only markersLeft and quoteName. Nothing exercises the clean-merge case (input nil, no round), the conflict.md cap and escaping,
      the preMerge mismatch errors (internal/worker/run.go:344), the leftover-marker hold, or a resolved round that sends MergeParent to the committer.
      Add them using the existing worker fakes.
  - file: "internal/reviewfix/loop.go"
    line: 389
    severity: high
    blocking: true
    message: |
      The new rule in mergeIncludes (the second parent must be an ancestor of the fetched main) is untested. plan.md names TestMergeIncludesSecondParentOnMain.
      This is the check that decides whether a conflict round landed. Add a case where the second parent is not on main (expect false) and one where it is (expect true).
      Also cover the fetch error path. The hard-coded "+refs/heads/main" also ignores a configured default branch, which the committer handles through cfg.Branch.
  - file: "internal/servecmd/serve.go"
    line: 738
    severity: medium
    blocking: false
    message: |
      plan.md asks for a servecmd test that merger.Deps.Rounds is set, and for an end-to-end test with the fakes
      (conflicting PR, conflict round, worker resolves, merge commit with main as second parent, reviewers re-run, plus an overlapping-edit case ending in needs-human).
      Neither was added. The wiring flips conflict handling from needs-human to automatic rounds, so one wiring test is worth adding.
      The prgate case (whitespace-only conflict resolution is not covered by #366) is also missing.
  - file: "internal/committer/merge.go"
    line: 31
    severity: low
    blocking: false
    message: |
      runMerge fetches main from l.cfg.Branch (default "main"), while worker/conflict.go and reviewfix/loop.go hard-code refs/heads/main.
      If a repository's default branch differs, the committer and the worker disagree about which tip is the merge parent. Use one source for the name.
---
The committer and gitx parts (merge-parent commit, tree invariant, lease push) look sound and are tested, but the worker conflict flow and the reviewfix landing check, both listed in plan.md's test plan, have no tests. This needs changes.

## Checks
- Lint: `make lint` ran. The only output I could see was a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, an existing testdata file the change does not touch. I found nothing on the changed files.
- Tests with coverage (`go test -cover` on committer, gitx, worker, reviewfix and servecmd): all pass. Coverage is 86.5%, 79.1%, 76.6%, 82.7% and 78.4%. Since conflict.go has only helper tests, conflictInput and preMerge in the worker are not exercised.
- Per-line diff coverage was not computed; I judged untested code from the test files in the diff.
