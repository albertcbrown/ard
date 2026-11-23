---
role: engineer
status: done
attempt: 1
---
Adds a `budget` loop that posts one status-issue comment per capped scope (repo or role) per UTC day and records a `budget_capped` event. The event is also the restart-proof marker, so a restart does not repeat a notice.

## Changes
- `internal/state/events.go`: new `EventBudgetCapped` kind (scope, stage, role, day, spent, cap, unpriced).
- `internal/budget/budget.go`: `Cap` and `Gate.Capped` list every reached cap with the same rules as `Denied`; the day-start code is shared.
- `internal/budget/notice.go`: the loop (mutex-guarded, no slot, `Claims()` false) and `Render`. The comment holds only numbers, the day and config names (unsafe names become `?`), no `@`. It says the figure is an estimate on a subscription and how to raise the cap. A failed comment writes no event and is retried next tick. Dry-run uses the same path.
- `internal/budget/testdata/*.golden`: repo, role and unpriced comments.
- `internal/servecmd`: `budgetLoop` registered last in `registerLoops`; `knownLoops` comment updated.
- `docs/proposal.md`: Budgets, Observability and `--loops` text updated.

## Testing
`go build ./...`, `gofmt -l`, and `go test ./internal/budget ./internal/servecmd ./internal/state` all pass. Added notice tests (once per day, restart, next UTC day, independent scopes, no status issue, comment failure, dry-run, event fields, goldens, no `@`), `TestCapped`, and a servecmd loop registration test. Updated the loop-count and known-loops tests.

## Open issues
- No servecmd test checks the real dry-run ledger file; dry-run is covered with a fake effects whose `DryRun()` is true and a negative issue number.
- I did not run `make lint` or the full `go test ./...`.
- A crash between posting the comment and recording the event can repeat one notice.
