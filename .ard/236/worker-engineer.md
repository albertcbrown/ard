---
role: engineer
status: done
attempt: 1
---
Enforces daily dollar caps (per role and whole repo, UTC day) at the pool's claim gate. A candidate at a cap gets a new `DeniedBudget` verdict and takes no slot; running jobs are untouched and deterministic scanners are exempt.

## Changes
- `internal/state`: migration 0007 (index on `step_usage.recorded_at`), golden schema, and `SpendSince` summing today's cost per (stage, role) and for the repo, with an unpriced-step count.
- `internal/config`: `Resolved.RoleBudgetUSDPerDay`; a role override of 0 or less counts as unset.
- `internal/pool`: `DeniedBudget` and `Options.Budget`, checked after the duplicate check, beside `Limited`.
- `internal/budget` (new): `Gate`; blocks at `spent >= cap`, fails closed on a read error, logs `budget.capped` once per cap per UTC day.
- `internal/servecmd`: `budgetGate` wiring (injectable clock for tests), `adoptPool` takes the hook, `budget` added to `knownLoops`.
- `docs/proposal.md`: Budgets, pool, Configuration and `--loops` text. `CLAUDE.md`: one Layout line (protected path).
- Tests: state, config, pool, budget, and serve-level tests with a real store and fake clock (role cap, repo cap, held job, restart, midnight reset, read failure, `--loops budget`).

## Testing
`go test ./...` passes; `gofmt` and `go vet` are clean. One existing test (schema version expectation) was updated from 6 to 7.

## Open issues
- A role with no cap of its own is limited only by the repo cap (assumed from the plan; change `RoleBudgetUSDPerDay` if the parent design says otherwise).
- `budget` in `--loops` is accepted but registers no loop: the gate is always on in the pool.
- The status-issue post on a cap is not implemented; only the log line.
- `CLAUDE.md` needs a human approval before merge.
