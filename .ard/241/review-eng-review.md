---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The token ledger, `tokens_per_day` config key, unreported-token mark and cost-table change match plan.md, and the new tests cover them. Approved; no new blocking problems were found after the merge from main.

## Checks
- Lint: `gofmt -l internal` reports only the untouched, deliberately broken `internal/roles/testdata/docs/skips/src/_x.go`. Nothing is reported on the changed files. `go build ./...` succeeds.
- Tests: `go test` for config, state, closeout, costblock, costs and reaper all pass.
- Diff coverage: no profile run. By reading, the added code is exercised by the new tests (ParseTokenCount, RoleTokensPerDay, RoleTokens, RoleWorstTokens, FactsOf, the costs row mark, the reaper mark cases).

## Previous findings
- internal/closeout/facts.go: resolved (the FactsOf round-trip is tested with and without the flag).
- internal/costs/rows.go: resolved (`TestRowMarksUnreportedTokens` covers a flagged and an unflagged job).
- internal/reaper/finish.go: resolved (the resumed close-out test shows one comment with the mark once).
