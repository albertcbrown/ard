---
role: engineer
status: done
attempt: 1
---
Adds the token ledger for per-role token caps: a `tokens_per_day` config key with a `TokenCount` parser, `RoleTokens` and `RoleWorstTokens` in the state store, and a "tokens not reported" mark on the issue for a job whose final agent step reported no tokens. No charge is recorded and nothing is gated.

## Changes
- `internal/config`: `TokenCount` (`40M`, `1.5M`, `500k`, plain integers), `tokens_per_day` at top level and per role, `RoleTokensPerDay`. Unset stays uncapped and renders unchanged.
- `internal/state`: `Tokens.Counted`, `RoleTokens` (input + output + cache write) and `RoleWorstTokens`. No schema change.
- `internal/closeout`: `Job.TokensUnreported`, a fixed line in the close-out comment, and the flag in `CostFacts`.
- `internal/costblock`, `internal/costs`: the cost-table row shows `not reported`, or `(partial)` when an earlier step did report.
- `internal/reaper/finish.go`: sets the flag and logs `reaper.tokens_unreported`.
- `docs/proposal.md`: config key and Budgets text that states the gap.
- Review round: added the three missing tests (below).

## Testing
Ran `go test ./internal/closeout/ ./internal/costs/ ./internal/reaper/`; all pass. Earlier runs passed for config, state and costblock.
- `TestTokensUnreportedMarkOnceOnResumeAndFacts` (closeout): the first `Run` fails, `ResumeJob` succeeds, one marker comment with one mark. `FactsOf` returns the flag, and returns no flag for an unflagged job.
- `TestRowMarksUnreportedTokens` (costs): one flagged and one unflagged closed job give the right row marks and a rendered `not reported` cell once.

## Review findings
- `internal/closeout/facts.go:36`: fixed: added the `FactsOf` round-trip test.
- `internal/costs/rows.go:47`: fixed: added the costs row-mark test.
- `internal/reaper/finish.go:255`: fixed: the resumed close-out test sits in the closeout package. It uses the real `Closer`; the reaper's test closer is a stub whose `ResumeJob` does nothing.

## Open issues
- `make lint` (`go vet ./...`) fails on the existing broken testdata file `internal/roles/testdata/docs/skips/src/_x.go`, which this change does not touch.
