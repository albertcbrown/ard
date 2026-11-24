---
role: engineer
status: done
attempt: 1
---
Adds the token ledger for per-role token caps: a `tokens_per_day` config key, per-role token totals in the state store, and a "tokens not reported" mark on the issue for jobs whose final agent step reported no tokens. Nothing is charged for such a job and nothing is gated.

## Changes
- `internal/config`: new `TokenCount` type (`40M`, `1.5M`, `500k`, plain integers; exact integer arithmetic), top-level and per-role `tokens_per_day`, and `RoleTokensPerDay` (role, then repo, then unset). The key is omitted from rendered output when unset.
- `internal/state/usage.go`: `Tokens.Counted` (input + output + cache write), `RoleTokens` (per stage and role since a day start) and `RoleWorstTokens` (largest per-job total from reported usage). No schema change.
- `internal/closeout`: `Job.TokensUnreported` (persisted with the record, so a resumed close-out shows the mark once), a fixed-text comment line, and a `CostFacts` field.
- `internal/costblock`, `internal/costs`: the row's tokens cell reads `not reported`, or gets ` (partial)` when an earlier step reported; the strict row pattern accepts only those forms.
- `internal/reaper/finish.go`: sets the flag for an agent or fix step with no reported tokens (not a test step, not `clone_failed`) and logs `reaper.tokens_unreported`.
- Docs: `docs/proposal.md` (config sample and Budgets text, including the overshoot gap) and `docs/site/configuration.md`.

## Testing
`go test ./...` passes. `go vet ./internal/...` is clean except a pre-existing parse error in a deliberately broken testdata file. Added: token parsing and config resolution tests, `RoleTokens` (day boundary, cache read excluded, NULL tokens, reopen) and `RoleWorstTokens`, reaper mark and no-mark cases with the log line and unchanged `RoleTokens`, a costblock render and edited-block rejection test, and a comment test.

## Open issues
- Not added: dedicated tests for `FactsOf` round-trip and the `costs` row mark, and a resumed-close-out test; the flag lives in the stored record, which `Run` and `ResumeJob` reuse, so I relied on that code reading rather than a test.
- The fake-clock day boundary is tested at the store level only (the caller supplies the UTC day start).
