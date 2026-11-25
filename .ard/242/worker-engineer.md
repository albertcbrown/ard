---
role: engineer
status: done
attempt: 1
---
Adds the daily token check to the claim gate, next to the dollar check, so a job must pass both. Adds a once-per-day token notice on the status issue and a token report for later status work.

## Changes
- `internal/budget/tokens.go` (new): `TokenReader`, `SetTokenReader`, the rule `U >= C or C-U < min(W,C)`, fail-closed read (`budget state unreadable`, WARN `budget.read_failed`), `budget.blocked` log once per role and UTC day, `TokenReport`, `TokenCapped`.
- `internal/budget/budget.go`: `Denied` runs the token check after the dollar checks; scanners with a scan path stay exempt; a nil reader skips the token check.
- `internal/budget/notice.go`: optional `Deps.Tokens`; one comment per token cap per UTC day with the marker `<!-- ard:budget cap=tokens role=<kind>/<name> day=<date> -->`, deduped by `budget_capped` events (scope `tokens`). The dollar notice is unchanged.
- `internal/servecmd/budget.go`: wires the store as token reader and the gate as token capper. `internal/pool/pool.go`: comments only.
- `docs/proposal.md`: Budgets, Configuration and Executors/Reaper text.
- `internal/budget/tokens_test.go` (new): rule table, per-role blocking, no-dollar role, repo default, both caps, scanner exempt, fail-closed, UTC reset, log once per day, nil reader, report, notices.

## Testing
`make test` passes (including internal/budget, servecmd, pool). `go vet ./internal/...` clean; gofmt clean.

## Open issues
- A maintainer should review the claim rule and the fail-closed path, as the issue asks.
- The token marker format differs from the existing dollar marker; both are kept.
