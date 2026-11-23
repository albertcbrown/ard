# Plan for #237

## Implementation

### Steps
1. `internal/state/events.go`: add `EventBudgetCapped = "budget_capped"` to the kinds, with a comment (data has `scope` "repo" or "role", `stage`, `role`, `day` as `2006-01-02` UTC, `spent_usd`, `cap_usd`, `unpriced`). No migration: `events` already takes any kind, and `Events(ctx, EventFilter{Kind, Since...})` can find it again (check `EventFilter` fields at lines 49-58 and use the existing time and kind filters; add no new method unless the filter cannot select by day, in which case filter the `data` JSON in Go). Reason: the event is both the #58 record and the restart-proof "already noticed" marker.
2. `internal/budget/budget.go`: add `type Cap struct { Scope, Stage, Role string; Spent state.Spend; Limit float64 }` (new) and `func (g *Gate) Capped(ctx context.Context) ([]Cap, time.Time, error)` (new). It reuses the day-start code and rules of `Denied` (repo cap first, then each configured non-exempt role, skipping deterministic scanners and roles missing from config), returns every capped scope plus the UTC `dayStart`, and does not log. Factor the shared day-start and cap-check code out of `Denied` so both use one rule; `Denied` behaviour and its tests stay unchanged.
3. `internal/budget/notice.go` (new): the loop.
   - `type Deps struct { Gate Capper; Store EventStore; Effects Effects; StatusIssue func() int; Log *slog.Logger; Now func() time.Time }`. `Capper` is `Capped` above; `EventStore` is `AppendEvent` and `Events`; `Effects` is `CreateComment(ctx, n int, body string) (github.Comment, error)` and `DryRun() bool`.
   - `type Loop struct`, `func New(d Deps) *Loop`, `Name() "budget"` (const `Name = "budget"`), `Interval` one minute, `Claims() false` (bookkeeping, like the costs loop, so pause does not matter... confirm with the `Claims` doc in `internal/controller/loop.go:27`).
   - `Tick`: if `StatusIssue()` is 0, return nil (not ready). Call `Gate.Capped`. For each cap, skip it if a `budget_capped` event for the same scope, stage, role and `day` already exists (read once per tick). Otherwise render the comment, `CreateComment` on the status issue, then `AppendEvent` with `At` now. A comment error is logged (`budget.notice_failed`) and retried next tick, with no event written. An event error after a posted comment is logged as an error. Dry-run goes through the same path: `Effects.CreateComment` writes the ledger, the synthetic negative status number is accepted, and the event is still recorded so one tick does not repeat.
   - Make `Tick` safe against overlap with a `sync.Mutex` so two ticks cannot both post.
   - `func Render(c Cap) string` and a fixed template with `text/template` or `fmt`: names the scope and role, the UTC day, `$spent of $cap` and the unpriced count when above zero, a fixed line that "on a subscription the dollar figure is an estimate", how to raise the cap (edit `budget_usd_per_day` in `.ard/config.yaml`, role override or repo level, through a reviewed PR), and that the cap clears at 00:00 UTC. It takes only numbers and the stage and role names from config, with no agent text, issue text or `@`. Start the comment with a marker like `<!-- ard:budget scope=... day=... -->` for humans and tests; keep the role name out of anything an `@` could come from by rejecting (replace with `?`) any config role name that is not `[a-z0-9._/-]+`.
4. `internal/budget/testdata/*.golden` (new): one golden each for the repo cap, a role cap, and a cap with unpriced steps; use the repo's golden convention (see `internal/state/golden_test.go` for the `-update` flag style).
5. `internal/servecmd/budget.go`: add `func budgetLoop(w *wiring) controller.Loop` (new) building `budget.New(budget.Deps{Gate: w.budgetGate(), Store: w.Store, Effects: w.Effects, StatusIssue: func() int { return int(w.statusIssue.Load()) }, Log: w.Log, Now: w.budgetNow})`. Reuse the gate from `budgetGate()` so the caps match what the pool enforces.
6. `internal/servecmd/serve.go`: append `budgetLoop(w)` in `registerLoops` (about line 440-460), and fix the `knownLoops` comment at line 84-86: `"budget"` now selects the notice loop, while the claim gate stays always on. Update the registerLoops doc comment.
7. `docs/proposal.md`: in the Budgets bullet (about line 450) replace "Posting the cap on the status issue is not part of this change." with the new behaviour: one comment per capped scope per UTC day on the status issue, kept across restarts by the `budget_capped` event, no agent text and no `@`, written to the ledger under dry-run. Update the `--loops` sentence there and in the Executors paragraph (`budget` now selects the notice loop; the gate is always on). Mention `budget_capped` in the Observability bullet (about line 453).

### Test plan
Command: `make test` (`go test ./...`), plus `make lint`.
- `internal/budget/notice_test.go` (fake clock, fake Capper, real `state.Store` in a temp dir, fake Effects recording comments):
  - `TestNoticeOncePerScopePerDay`: two ticks with the repo capped, one comment and one `budget_capped` event.
  - `TestNoticeAcrossRestart`: tick, build a new Loop on the same store, tick again: still one comment.
  - `TestNoticeNextUTCDay`: the clock passes 00:00 UTC while still capped: a second comment, with `day` changed.
  - `TestNoticeScopesIndependent`: repo cap plus two role caps give three comments, each once.
  - `TestNoticeNoStatusIssue`: `StatusIssue()==0` posts nothing, writes no event, and posts on the later tick.
  - `TestNoticeCommentFails`: the error is logged, no event is written, and the next tick retries.
  - `TestNoticeDryRun`: `DryRun()` true with a negative status number still comments and records the event. Also an `internal/servecmd` test with the real dry-run `effects` checks the comment lands in the JSONL ledger.
  - `TestNoticeEventFields`: event kind, scope, stage, role, day, spent and cap are all present (#58).
  - `TestRenderGolden` for the three goldens.
  - `TestRenderNoAtNoAgentText`: output has no `@`, even with a hostile role name; the estimate sentence and the raise-the-cap text are present.
- `internal/budget/budget_test.go`: `TestCapped` covers repo and role caps, an exempt scanner, a missing role, a read error, and a `Denied` unchanged check.
- `internal/servecmd/budget_test.go`: `--loops budget` registers a loop named `budget`; `registerLoops` includes it; `TestKnownLoops`-style tests in `serve_test.go` and `loops_test.go` are updated for the new count.
- Edge cases: spend exactly at cap (capped), a UTC midnight boundary, an event written by yesterday's day does not suppress today's.

### Risk
Low to medium. The new loop only reads spend and writes one comment and one event, and it takes no slot and does not change the claim gate. The risks are a duplicate comment if the process dies between the comment and the event (the window is small, and the alternative, losing a notice, is worse), and comment text built from config names (guarded by the name check and the no-`@` test). It adds a stored event kind but no migration. No file in `merge.protected_paths` is touched: only `internal/` and `docs/proposal.md`. Concurrency: one mutex in the loop.

### Size
M (about 350 lines, as the issue says). Files: `internal/state/events.go`, `internal/budget/budget.go`, `internal/budget/notice.go`, `internal/budget/notice_test.go`, `internal/budget/budget_test.go`, `internal/budget/testdata/*.golden`, `internal/servecmd/budget.go`, `internal/servecmd/budget_test.go`, `internal/servecmd/serve.go`, `internal/servecmd/serve_test.go` or `loops_test.go`, `docs/proposal.md`.
