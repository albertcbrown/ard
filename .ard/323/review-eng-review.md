---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/worker/run.go"
    line: 626
    severity: high
    blocking: true
    message: |
      The conflict-round diff guard (facts measured against mainTip) has no test, though it is a "Done when" item of the issue and plan.md names TestConflictRoundDiffGuardOwnChanges.
      Nothing shows that main's own changes beyond diff_max_lines pass while the PR's own oversize changes are rejected.
      Add both cases with the existing worker harness.
  - file: "internal/worker/run.go"
    line: 720
    severity: medium
    blocking: false
    message: |
      The GitHub workflow-rejection path (needs-human, no retry) has no test; plan.md lists TestConflictWorkflowRejectedNeedsHuman.
      Matching any error text that contains "workflow" is also loose. Also untested: TestConflictRoundNoGitInAgentTree, the MaxFiles over-limit hold in push(), and TestConflictRoundOverlapNeedsHuman.
  - file: "internal/committer/merge.go"
    line: 25
    severity: medium
    blocking: false
    message: |
      runMerge is at 68.8% coverage. plan.md's committer cases TestCommitCodeMergeNonLeasePush, ...UndeclaredDiffRefused and ...ForbiddenDeclared were not written as such,
      and the stale-lease path (ErrConflict when the push is rejected) and the undeclared-diff guard are not exercised.
      The issue's "Done when" asks for tests that the committer refuses a non-lease push and any path CheckCodePath rejects. Add a test for the push-rejected branch and one for an undeclared difference.
  - file: "internal/servecmd/serve.go"
    line: 739
    severity: medium
    blocking: false
    message: |
      There is no test that merger.Deps.Rounds is wired, and no end-to-end test (conflicting PR, round, merge commit with main as second parent, reviewers re-run, plus an overlapping-edit case ending in needs-human).
      The prgate whitespace-only-resolution case is also missing. Both were in plan.md's test plan and the issue's first "Done when" item. The wiring switches conflict handling from needs-human to automatic rounds, so add at least a wiring test.
  - file: "internal/reviewfix/loop.go"
    line: 403
    severity: low
    blocking: false
    message: |
      reviewfix and the worker (conflict.go:53) hard-code refs/heads/main, while the committer's runMerge uses cfg.Branch. Use one source for the default branch name (still present from the previous round).
      The plan asked for the second parent to be on main's first-parent history; the code checks IsAncestor on main, which is weaker (any commit merged into main passes).
---
The earlier worker and reviewfix test gaps are now covered, and the committer and gitx merge path looks sound. This still needs changes because the diff guard for conflict rounds, a "Done when" item, has no test.

## Checks
- Lint: `make lint` printed only a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, which the change does not touch. Nothing on the changed files.
- Tests with coverage on committer, gitx, worker, reviewfix and servecmd all pass. Coverage is 86.5%, 79.1%, 82.0%, 83.1% and 78.4%. Per function: runMerge 68.8%, conflictInput 85.4%, commitParents 78.6%, MergeNoCommit 84.2%.

## Previous findings
- internal/worker/conflict.go (conflict flow tests): resolved. conflict_round_test.go covers input, clean merge, preMerge mismatch, resolved round and markers left.
- internal/reviewfix/loop.go (mergeIncludes test): resolved. The second-parent-on-main cases and the fetch error are tested. The hard-coded branch name remains, see the low finding.
- internal/servecmd/serve.go (wiring and end-to-end tests): still present.
- internal/committer/merge.go (branch name source): still present, now reported on loop.go.
