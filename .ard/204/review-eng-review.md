---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/config/load.go"
    line: 85
    severity: low
    message: |
      The `minLLMScannerEvery` const still sits between the doc comment of `applyOverrides` and the function, so that comment now precedes the const.
      Move the const above the comment.
  - file: "internal/scanner/pipeline.go"
    line: 383
    severity: medium
    message: |
      The near-duplicate block in `Run` (views to TitledIssue, `res.NearDup`, the `scanner.near_duplicate` log) is still not executed by any test.
      `Run` still skips LLM scanners, so it is dead code today and the helper `SkipNearDuplicates` is covered. Add a test with the engine PR, or extract a helper that builds `[]TitledIssue` from views.
---
The only commit since the last round is a merge from main. The change matches PR 1 of the plan and I found no correctness bug, so I approve. The two earlier non-blocking findings are still present.

## Checks
- Lint: not re-run. `make lint` failed last round on a gofmt fixture the change does not touch.
- Diff coverage: not re-run. The code is unchanged since the last round, when the new helpers were at 100%. The near-duplicate block in `Pipeline.Run` is not executed.

## Previous findings
- internal/config/load.go (const placement): still present
- internal/scanner/pipeline.go (near-duplicate wiring untested): still present
- internal/config/load.go (budget/every deviation from plan): no longer applicable; the worker's explanation holds and the deviation is not blocking
