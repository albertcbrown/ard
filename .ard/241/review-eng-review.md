---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The token ledger, config key, unreported-token mark and cost-table change match plan.md. The three tests the previous round asked for now exist and pass, so this is approved.

## Checks
- Lint: `make lint` runs `go vet ./...`, which in the earlier round failed only on the untouched deliberately broken testdata file `internal/roles/testdata/docs/skips/src/_x.go`. This round `gofmt -l internal` reports only that same file. Nothing is reported on the changed files.
- Tests: `go test` for config, state, closeout, costblock, costs and reaper all pass.
- Diff coverage: no profile run. By reading, the added code is exercised by the new tests, including FactsOf, the costs row mark, the resumed close-out and the reaper mark cases.

## Previous findings
- internal/closeout/facts.go: resolved (`TestTokensUnreportedMarkOnceOnResumeAndFacts` checks the FactsOf round-trip with and without the flag).
- internal/costs/rows.go: resolved (`TestRowMarksUnreportedTokens` checks one flagged and one unflagged job).
- internal/reaper/finish.go: resolved (the resumed close-out test shows one comment with the mark once).
