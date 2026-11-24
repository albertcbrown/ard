# Plan for #58

## Implementation

### Steps
1. `internal/state/events.go` (edit): add `EventDigest = "digest"` (data: `day`, `jobs`, `failures`, `spend_usd`, `unpriced`). It is the once-per-UTC-day marker, as `EventBudgetCapped` is for notices. Document it next to the other kinds.
2. `internal/state/jobs.go` (edit): add `func (s *Store) CountEndedSince(ctx, since, until time.Time) (map[RoleKey]JobCounts, error)` with new `type JobCounts struct{ Done, Failed int }`. It groups jobs by `stage, role, status` where `status != 'running'` and `ended_at` is in `[since, until)`, same pattern as `EndedSince`. Reason: the digest needs jobs run and failures per role without loading every row.
3. New package `internal/digest/` (`digest.go`, `render.go`):
   - `const Name = "digest"`.
   - `type Deps struct{ Store Store; Reader Reader; Effects Effects; StatusIssue func() int; Log *slog.Logger; Now func() time.Time }`.
   - `Store` is a small interface: `Events`, `AppendEvent`, `EndedSince`/`CountEndedSince`, `SpendSince` (the first two are as in `internal/budget/notice.go`).
   - `Reader` is `ListIssues(ctx, github.ListIssuesOpts) ([]github.Issue, error)`.
   - `Effects` is `CreateComment` and `DryRun`.
   - `NewLoop(Deps) *Loop` implements `controller.Loop`: `Name()`, `Interval()` = 10 minutes, `Claims()` false (bookkeeping, runs when paused).
   - `Tick` returns early when `StatusIssue()` is 0, or when an `EventDigest` event with today's `day` already exists since the UTC day start.
   - The digest covers the previous complete UTC day: `[dayStart-24h, dayStart)`. Jobs, failures and spend come from the Store; `SpendSince` only takes a start, so add `SpendBetween(ctx, from, to)` in `internal/state/usage.go` by factoring the query, and keep `SpendSince` as a wrapper.
   - Queue depth: `Reader.ListIssues` with `State: open` and `Labels: []string{label}` per `ard:` label, counting issues (not PRs). Use a fixed list from `internal/labels`: `Pending`, `NeedsHuman`, `NeedsApproval`, `Hold`, `ExecutorFailed`, `TestsFailed`, plus the stage/role request labels the config defines. Alternative: one `ListIssues` open call and count `Labels` that start with `labels.Prefix`. Prefer the single call: one request, no list to maintain.
   - Post with `Effects.CreateComment(issue, Render(...))`, then append `EventDigest`. Post first, record second, so a crash can only repost, never skip. Skip when `DryRun()` after logging `digest.notice`.
   - `Render(d Data) string`: first line `<!-- ard:digest day=YYYY-MM-DD -->`, a title, a queue-depth table (label, count), jobs run, failures (count, plus per-role table), spend in USD with an unpriced count and the on-a-subscription-estimate note. Only numbers, the day and label/role names that pass a safe-name regexp (copy `safe` from `internal/budget/notice.go`); no agent text, no `@`, no issue titles.
4. `internal/servecmd/digest.go` (new): `func digestLoop(w *wiring) controller.Loop` building `digest.NewLoop` with `w.Store`, the GitHub reader used by `triageLoop`, `w.Effects`, `w.statusIssue.Load`, `w.Log`, `w.budgetNow`.
5. `internal/servecmd/serve.go` (edit): append `digestLoop(w)` in `registerLoops` after `budgetLoop(w)` (~line 461); add `digest.Name` to `knownLoops` (line 89); update the `registerLoops` doc comment.
6. `docs/proposal.md` (edit, Observability bullet at ~line 477): describe the digest, with the title shape, the previous-UTC-day window, the `digest` event and the once-per-day marker. No README change is needed unless the README lists loops (check).
7. `docs/TODO.md`: leave alone (a maintainer already noted it is stale).

### Test plan
Command: `make test` (`go test ./...`), plus `make lint`.
- `internal/digest/digest_test.go`, with fake Store, Reader and Effects:
  - `TestTickPostsOnce`: first Tick posts one comment and appends one `digest` event; a second Tick posts nothing.
  - `TestTickNoStatusIssue`: `StatusIssue()==0` gives no post and no event.
  - `TestTickRestartKeepsMarker`: a new Loop over the same store posts nothing for the same day; the next UTC day posts again.
  - `TestTickPostFailsNoEvent`: `CreateComment` error leaves no event, so the next Tick retries; the error is logged.
  - `TestTickDryRun`: nothing is posted and no event is stored.
  - `TestWindowPreviousDay`: jobs/spend before or after `[dayStart-24h, dayStart)` are excluded; a job ending exactly at `dayStart` is excluded.
  - `TestQueueDepthByLabel`: open issues with labels `ard:pending` and `ard:hold` count per label; PRs and non-`ard:` labels are ignored.
  - `TestRenderGolden` (`internal/digest/testdata/`): zero activity gives a digest of zeros, not an empty comment. Include an unpriced step and a role name that fails `safe` (renders `?`); the output has no `@`.
  - `TestIntervalAndClaims`: `Claims()` false.
- `internal/state/jobs_test.go`: `TestCountEndedSince` covers running jobs excluded, done and failed split, and the grouping by stage and role. `internal/state/usage_test.go`: `TestSpendBetween` checks the half-open bounds and that `SpendSince` still passes.
- `internal/servecmd/serve_test.go`: `selectLoops` accepts `digest`; any test comparing the loop list or `knownLoops` is updated.
- Edge: a Reader error means a Tick error and no post (a digest with a missing section is worse than a delayed one).

### Risk
Low. It adds one read-only loop that posts a comment. No protected paths under `merge.protected_paths` are touched (`docs/` and `internal/` are not listed). No migration: only new queries on existing tables and one new event kind. The public interface change is a new `--loops` name. Security: the comment is public and must hold numbers and safe names only. Concurrency: one loop with a mutex, as in the budget loop. One extra open-issues list call per 10 minutes is an API cost; the once-a-day check should run before the list call.

### Size
M (about 250 to 300 changed lines including tests; likely L at the upper end because of tests and golden data). Files: `internal/state/events.go`, `internal/state/jobs.go`, `internal/state/usage.go`, `internal/state/jobs_test.go`, `internal/state/usage_test.go`, `internal/digest/digest.go` (new), `internal/digest/render.go` (new), `internal/digest/digest_test.go` (new), `internal/digest/testdata/` (new), `internal/servecmd/digest.go` (new), `internal/servecmd/serve.go`, `internal/servecmd/serve_test.go`, `docs/proposal.md`.
