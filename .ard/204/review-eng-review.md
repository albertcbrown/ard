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
      The new `minLLMScannerEvery` const was inserted between the doc comment of `applyOverrides` and the function.
      The "applyOverrides merges the global role defaults..." comment now documents the const's neighbour, not the function.
      Move the const above that comment.
  - file: "internal/scanner/pipeline.go"
    line: 383
    severity: medium
    message: |
      The near-duplicate block in `Run` (views to TitledIssue, `res.NearDup`, `scanner.near_duplicate` log) is not executed by any test.
      `Run` still skips LLM scanners, so this wiring is dead code today. The worker disclosed this and `SkipNearDuplicates` itself is covered.
      Add a test with the engine PR, or a small helper that builds the `[]TitledIssue` from views so the wiring can be tested alone.
  - file: "internal/config/load.go"
    line: 130
    severity: low
    message: |
      plan.md asked for an error when an LLM scanner has no `max_budget_usd`, and for an `every` below 24h to be rejected when unset.
      The worker instead raises the default `every` to 24h and errors only on an explicit non-positive budget, because the budget is inherited from the validated global value.
      The reasoning holds, so this is not blocking. The plan's `ard config check` and servecmd loader tests were also not added; both share the loader that the config tests cover.
---
The change matches PR 1 of the plan: config rules, fingerprint grammar, near-duplicate skipping, footer and the wider stale closer are implemented and tested, and I found no correctness bug. Approved. The remaining findings are small. The near-duplicate wiring in `Run` is dead code until the engine lands, and it has no test yet.

## Checks
- Lint: not run. `make lint` fails on a gofmt fixture the change does not touch, as the worker reported. Tests: `go test` with coverage on `internal/config`, `internal/scanner` and `internal/scanner/stale` passes.
- Diff coverage: `ParseLLMFingerprint`, `CheckLLMFingerprints`, `LLMGrammarLines`, `SimilarTitles`, `SkipNearDuplicates`, `createBody` and `staleCandidate` are at 100%. The near-duplicate block in `Pipeline.Run` is not executed.
