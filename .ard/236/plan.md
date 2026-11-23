# Plan for #236

## Implementation

### Steps
1. `internal/state/migrations/0007_budget.sql` (new): `CREATE INDEX step_usage_recorded ON step_usage(recorded_at)`; regenerate `internal/state/testdata/schema.golden` with `go test ./internal/state -run Golden -update`. Reason: today's spend query scans by `recorded_at` (UTC seconds, written in `internal/state/usage.go`).
2. `internal/state/usage.go`: add `type Spend struct { USD float64; Unpriced int }` and `func (s *Store) SpendSince(ctx context.Context, dayStart time.Time) (perRole map[RoleKey]Spend, repo Spend, err error)` (new; `RoleKey{Stage, Role string}`). It joins `step_usage u` to `jobs j` on `job_id`, filters `u.recorded_at >= dayStart.Unix()`, and groups by `j.stage, j.role`. `COALESCE(SUM(cost_usd),0)` gives USD, and `SUM(cost_usd IS NULL)` gives the unpriced count. Keep the key as (stage, role), because `jobs.role` alone collides (triage/engineer vs workers/engineer). Scanner-stage rows count toward the repo total and the per-role totals (deterministic scanners cost $0 anyway).
3. `internal/config`: add `func (r *Resolved) RoleBudgetUSDPerDay(role Role) (cap float64, set bool)` (new, in `types.go` or a new `budget.go`). It returns `role.BudgetUSDPerDay` when > 0, and otherwise reports no per-role cap. Resolved.BudgetUSDPerDay stays the repo cap; the existing override parse in `load.go:360` is reused. Document that an unset role has no separate cap (only the repo cap applies). If the #57 plan instead says an unset role inherits the repo figure as its own cap, change this one function.
4. `internal/pool/pool.go`: add `DeniedBudget` after `DeniedLimited`, and `"budget"` to `verdictNames`. Add `Options.Budget func(c Candidate) (denied bool, reason string)` and a `budget` field on `Pool`, wired in `New`. In `Offer`, next to the `p.limited` check at about line 173, set `DeniedBudget` with the reason, take no slot and reserve no demand. Held keys are still `DeniedDuplicate` and are checked first, so a running job is untouched.
5. `internal/budget/budget.go` (new package): `type Gate struct`, `func New(store SpendReader, cfg func() *config.Resolved, now func() time.Time, log *slog.Logger) *Gate`, and `func (g *Gate) Denied(c pool.Candidate) (bool, string)`. `SpendReader` is a small interface that `*state.Store.SpendSince` satisfies. The gate computes the UTC day start, reads spend (cached for a few seconds per day is optional, and the day boundary must invalidate it), and applies these rules:
   - Deterministic scanners (`Kind==KindScanner && ScanPath != ""`) and roles missing from the config are exempt.
   - Deny when `repoSpent >= cfg.BudgetUSDPerDay`, or when the role's spend `>= its cap`.
   - A read error denies (fail closed) and is logged.
   - Unpriced steps add $0, and the count is included in the reason and the log.
   - Each cap is logged once per UTC day as `budget.capped` (scope, role, spent, cap, unpriced), tracked in a map reset at the day change.
   Add one Layout line for `internal/budget/` to `CLAUDE.md`. This is a protected path, so a human approves it.
6. `internal/servecmd`: in `reaping.go` or a new `budget.go`, add `func (w *wiring) budgetGate() *budget.Gate`, built once under `initMu` like `limitsBook`. Pass `gate.Denied` through `adoptPool` (`adopt.go:59` gains a `budget` parameter and a `pool.Options.Budget`). Update `adoptPool` callers and tests. Add `"budget"` to `knownLoops` (`serve.go:86`); see Open questions on what that name registers.
7. `docs/proposal.md`: update Budgets (about line 444: block at `spent >= cap`, UTC day, unpriced counts as $0 and is shown, no reviewer exemption, enforced on OAuth, deterministic scanners exempt, running jobs finish), the pool text (`DeniedBudget`, no slot taken), and Configuration (about lines 394 and 422: `budget_usd_per_day` per role and repo semantics). It also mentions `budget` in the loops list.

### Test plan
Command: `make test` (`go test ./...`).
- `internal/state/usage_test.go`:
  - `TestSpendSince`: rows on both sides of 00:00 UTC, two roles, one NULL cost. Expect per-role and repo sums for today only, and Unpriced = 1.
  - `TestSpendSinceSurvivesReopen`: close and reopen the store, same totals.
- `internal/config`: `TestRoleBudgetUSDPerDay` covers an override set, an override unset, and a role absent from the config.
- `internal/pool/pool_test.go`: `TestBudgetVerdict` returns `DeniedBudget` with the reason and no slot, lower classes are not held back, a held key stays `DeniedDuplicate`, and `String()=="budget"`.
- `internal/budget/budget_test.go` (fake clock, fake reader):
  - spent == cap blocks and spent just under cap passes.
  - Role cap denies that role only.
  - Repo cap denies all agent roles but not a deterministic scanner.
  - An LLM scanner is denied.
  - Reviewers are not exempt.
  - Reader error denies.
  - `budget.capped` is logged once per day, and logged again after the UTC day rolls over.
  - Unpriced count appears in the reason.
- `internal/servecmd/budget_test.go` (serve level, real state store, fake clock):
  - Reaching a role cap gives `DeniedBudget` for that role only.
  - Reaching the repo cap denies every agent role but not a deterministic scanner.
  - A job already holding a slot keeps it and finishes.
  - The cap clears at 00:00 UTC and survives a restart (new wiring on the same store).
  - A state read failure fails closed.
  - `--loops budget` is accepted.
- Edge cases: costs exactly at midnight (`recorded_at == dayStart` counts for the new day), a zero-cap role (config rejects <= 0 for the repo; a role override of 0 means unset).

### Risk
Medium. The change gates every claim and so touches the scheduling path, and a bug could stop all work (fail closed is intended). It adds a migration (additive index only) and a new pool verdict (an internal interface). `CLAUDE.md` is in `merge.protected_paths`, so the PR waits for a human approval. No concurrency primitives beyond the existing `initMu` and the gate's own mutex for the logged-once map.

### Size
L (about 600 lines, as the issue says). Files: `internal/state/migrations/0007_budget.sql`, `internal/state/testdata/schema.golden`, `internal/state/usage.go`, `internal/state/usage_test.go`, `internal/config/types.go` (or `budget.go`), its test, `internal/pool/pool.go`, `internal/pool/pool_test.go`, `internal/budget/budget.go`, `internal/budget/budget_test.go`, `internal/servecmd/adopt.go`, `internal/servecmd/reaping.go` or `budget.go`, `internal/servecmd/serve.go`, `internal/servecmd/budget_test.go`, `CLAUDE.md`, `docs/proposal.md`.
