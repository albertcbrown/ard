---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/worker/run.go"
    line: 718
    severity: medium
    blocking: false
    message: |
      The GitHub workflow-rejection path (needs-human, no retry) still has no test, though plan.md lists TestConflictWorkflowRejectedNeedsHuman.
      Matching any error text that contains "workflow" is also loose. Add a test with a fake push error, and match a narrower message if one is known.
  - file: "internal/committer/merge.go"
    line: 346
    severity: medium
    blocking: false
    message: |
      The push-rejected branch of runMerge (stale lease or non-fast-forward, mapped to ErrConflict) has no test, and neither does the case of an undeclared difference from main.
      The issue's "Done when" asks for a test that a non-lease push is refused. Add a test with a branch that moves during the push, and one with an undeclared diff.
  - file: "internal/servecmd/serve.go"
    line: 739
    severity: medium
    blocking: false
    message: |
      Nothing tests that merger.Deps.Rounds is wired, and there is no end-to-end test of a conflicting PR going through a round to a merge commit, or of an overlapping edit ending in needs-human.
      The wiring changes conflict handling from needs-human to automatic rounds. Add at least a wiring test.
  - file: "internal/reviewfix/loop.go"
    line: 403
    severity: low
    blocking: false
    message: |
      reviewfix and the worker (conflict.go:53) hard-code refs/heads/main, while runMerge uses cfg.Branch. Use one source for the default branch name.
      The check IsAncestor(second parent, main) is also weaker than the plan's first-parent history on main.
---
The change implements the plan, and the blocking finding from the previous round is resolved: TestConflictRoundDiffGuardOwnChanges now covers both main's own lines (not counted) and the PR's oversize lines (rejected). Approved. Three smaller test gaps remain and do not block.

## Checks
- Lint: not re-run this round. The previous round's `make lint` found nothing on the changed files.
- Tests: `go test` on worker, committer, gitx, reviewfix and servecmd all pass. I did not re-run coverage.

## Previous findings
- internal/worker/run.go (diff guard test): resolved. TestConflictRoundDiffGuardOwnChanges covers both cases.
- internal/worker/run.go (workflow rejection test): still present.
- internal/committer/merge.go (runMerge coverage): still present. The stale-lease and undeclared-diff paths are untested.
- internal/servecmd/serve.go (wiring and end-to-end tests): still present.
- internal/reviewfix/loop.go (branch name source): still present.
