---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The token ledger, `tokens_per_day` config, unreported-token mark and cost-table change match the issue's done-when list and are covered by new tests. Approved; no blocking problems found.

## Checks
- Lint: the repository has no lint entry point that I ran. I used `gofmt -l internal`, which reports only the untouched, deliberately broken `internal/roles/testdata/docs/skips/src/_x.go`. `go build ./...` succeeds.
- Tests: `go test` for config, state, closeout, costblock, costs and reaper all pass.
- Diff coverage: no profile run. By reading, the added code is executed by the new tests (ParseTokenCount, RoleTokensPerDay, RoleTokens, RoleWorstTokens, FactsOf, the costs row mark, and the reaper mark cases including test step and clone_failed).

## Previous findings
- internal/closeout/facts.go: resolved
- internal/costs/rows.go: resolved
- internal/reaper/finish.go: resolved
