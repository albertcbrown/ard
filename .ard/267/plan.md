# Plan for #267

## Implementation

### Steps
1. **Move check evaluation (no behaviour change).** New `internal/prgate/checks.go` (package `prgate`) receives from `internal/merger/gates.go`: the `checkState` type and its constants, `runState`, `newer`, `statusState`, `newerStatus`, `combine` and `evalChecks`. Export them: `type CheckState int` with `ChecksGreen`, `ChecksPending`, `ChecksRed` (keep `String()`), and `func EvalChecks(required []github.RequiredCheck, runs []github.CheckRun, statuses []github.CommitStatus, graceOver bool) (CheckState, []string)`. `RunState` and `Combine` are exported only if tests need them. Leave `behind`, `closingRef` and `approved` in the merger. Move the check cases of `internal/merger/gates_test.go` to `internal/prgate/checks_test.go` with their bodies unchanged; only the package and identifier prefixes change.
2. **Switch the merger.** In `internal/merger/{gates,merge,loop}.go` (and tests that reference the old names) replace the old identifiers with `prgate.EvalChecks` and `prgate.Checks*`. Delete the moved code from `gates.go`. The build and merger tests must pass unchanged in meaning.
3. **Config keys.** In `internal/config`:
   - `types.go`: `Intervals.PRWatch Duration` (default 5m) and a new `PRWatch struct{ PendingTimeout time.Duration }` field on `Resolved` (default 2h).
   - `defaults.go`: add `defaultIntervalPRWatch` and `defaultPRWatchPendingTimeout`. Resolve `intervals.prwatch` with `intervalOr`, and add it to the interval validation list.
   - `defaults.go`: validate `pr_watch.pending_timeout >= intervals.prwatch` and `<= 7d`. The error key is `pr_watch.pending_timeout`, in the existing `Errors` style.
   - `raw.go`: add `prwatch` to `rawIntervals`, plus a top-level `pr_watch` case (a mapping with `pending_timeout`, parsed with the same duration decoder). Unknown keys are rejected, as elsewhere.
   - `render.go`: print both keys in the `ard config check` YAML (`intervalsYAML.PRWatch`, new `pr_watch` block).
   - Add cases to `load_test.go`, `duration_test.go` and `render_test.go`, and update golden files in `testdata/` if the render tests use them.
4. **State.** Add `internal/state/migrations/0008_pr_watch.sql`, creating `pr_watch(pr INTEGER PRIMARY KEY, issue INTEGER, head TEXT, checks_state TEXT, head_seen_at TEXT, pending_notified_head TEXT, red_notified_head TEXT, closed_notified INTEGER NOT NULL DEFAULT 0, merge_recorded INTEGER NOT NULL DEFAULT 0)`. Check the existing migration naming and how `golden_test.go` pins the schema, and update the golden. Add store methods (new, in `internal/state/prwatch.go`): `GetPRWatch(ctx, pr)`, `PutPRWatch(ctx, row)` and `DeletePRWatch`. In `internal/state/events.go` add `EventPRWatch = "pr_watch"` (data `{pr, head, transition}`). The existing `EventMerge` is reused for human merges with data `{pr, head, merge_sha, actor}`; check that the merger's own `merge` events carry the same `pr` and `head` keys.
5. **New package `internal/prwatch`** (`doc.go`, `loop.go`, `watch.go`), modelled on `internal/merger/loop.go`:
   - `Reader` interface: `Identity`, `ListPRs`, `GetIssue`, `RequiredChecks`, `ListCheckRuns`, `ListCommitStatuses`, `ListComments`, and `GetPermission` only if needed.
   - `Effects` interface: `AddLabels`, `CreateComment`, `DryRun`. No merge, branch-update or review calls, which enforces the ownership rule at the type level.
   - `Store` interface: `RunningJobs`, `AppendEvent`, `Events`, and the new `pr_watch` methods.
   - `Loop`: `Name()` returns `"prwatch"`. `Interval` reads `c.Intervals.PRWatch`, default 5m. `TickTimeout` is 5m. `Claims()` returns true. It takes no pool slot.
   - `Tick`: one `ListPRs(state=all, base=main)`, filtered with `prgate.ParseWorkerBranch` and author == App login. Fetch `RequiredChecks` once. Skip a PR whose issue has a running job in `RunningJobs`.
   - Per PR, classify state: draft, ready, merged or closed.
   - Checks: a settled (green or red) `prgate.EvalChecks` result is cached per head SHA in the `pr_watch` row and in memory. Call `ListCheckRuns` and `ListCommitStatuses` only for open PRs whose head is not settled, so the cost is at most 1+2N calls. `graceOver` is true, since prwatch only cares about required-check pending time. Without required checks, use the same rule as the merger.
   - Actions:
     - (a) Closed, unmerged, issue open, no merge recorded, `closed_notified` unset: `AddLabels(issue, "ard:needs-human")` plus one comment with retry steps (remove `ard:worker:<role>:done`) or close the issue.
     - (b) Pending on the same head longer than `pr_watch.pending_timeout` since `head_seen_at`, and `pending_notified_head != head`: one status-issue notice naming the pending checks as sanitized code spans. A head change resets `head_seen_at`. Find the status issue the way `internal/budget` finds it.
     - (c) Merged by someone other than the App, with no `merge` event for (pr, head): append `EventMerge` with the human actor and log. No comment.
     - (d) Draft PR with red required checks on a head not yet notified: one PR comment naming the failed checks. Skip ready PRs.
   - Idempotence: before acting, look for an existing `pr_watch` event for (pr, head, transition) and for the comment marker `<!-- ard:prwatch <transition> head=<sha> -->` in `ListComments`. Write the event and row after the effect.
   - Sanitize check names: strip backticks, control characters and `@`, cap the length. Bodies carry no PR or issue text.
   - Dry-run (`Effects.DryRun()`): perform effects only (which write the ledger), append no events and write no rows. Use an in-process `seen` set to log each transition once.
   - Other changes (head moved, conflicts, review requests, comments, label edits): `slog` only.
6. **Wire into serve.** Add `internal/servecmd/prwatch.go` with `prwatchLoop(w *wiring)`, like `mergerLoop`. In `internal/servecmd/serve.go`, add `"prwatch"` to `knownLoops` (after `"merger"`) and call it in `registerLoops`. Update the `serve_test.go` expectations on loop lists and names.
7. **Docs.** Add the prwatch entry to the CLAUDE.md layout (protected path). In `docs/proposal.md`: a "PR watch loop" section after the merger; a pointer from the merger section; the config block with `intervals.prwatch` and `pr_watch.pending_timeout`; and the new `needs-human` source in the label state machine.

### Test plan
Run `make test`, then `make lint`. Table-driven tests with the existing GitHub fake (see `internal/merger/harness_test.go` for the pattern). No network.
- `internal/prgate/checks_test.go`: the moved merger check cases, unchanged. New case `statuses_only_green_never_pending`: required contexts reported only as green commit statuses give `ChecksGreen` (#256 regression).
- `internal/config`: defaults (5m, 2h); `intervals.prwatch: 0s` or below the minimum is an error; `pending_timeout` below the interval or above 7d is an error; a bad type is an error; `config check` output prints both.
- `internal/state`: migration applies; row round trip; golden schema.
- `internal/prwatch/watch_test.go`:
  - `closed_unmerged`: needs-human label plus one comment. A second tick and a fresh `Loop` over the same store add nothing more.
  - `closed_issue_closed` and `closed_but_merged`: no action.
  - `pending_under_timeout`: nothing. `pending_over_timeout`: exactly one notice across repeated ticks and a restart. `new_head_resets_clock`.
  - `human_merge`: one `merge` event with the human actor. `ard_merge`: no new event.
  - `draft_red`: one comment per head, a second comment on a new head. `ready_red`: nothing.
  - `settled_head_zero_check_calls` and `max_calls_1_plus_2N` (counting fake calls).
  - `running_job_skips`, `dry_run_writes_ledger_only`, `non_worker_or_foreign_author_ignored`.
  - `sanitize`: check names with backticks, `@` and newlines come out safe.
  - `claims_true` and `interval_default`.
- `internal/servecmd`: `--loops prwatch` is accepted and registration order is as expected.
- Failure cases: a GitHub error on one PR is logged and the others are still processed; an effect failure leaves no event, so it retries next tick.

### Risk
Medium. It adds a loop that writes to GitHub, a SQLite migration and a new config key. The check move is behaviour-neutral but sits on the merge path, so the merger tests must pass unchanged. The loop only labels and comments, never merges. Public interface changes are limited to the config schema (additive). The PR touches the protected path CLAUDE.md, so it needs a human approval. Duplicate-notice risk is handled by the event, row and marker triple check. There is no new concurrency beyond the loop's own state, which is guarded by a mutex like the merger's.

### Size
L, about 1,200 changed lines with tests, below the 2,000-line block limit. If it grows past the warn limit, the worker may split along the issue's A/B line. Files: `internal/prgate/checks.go` (new), `internal/prgate/checks_test.go` (new), `internal/merger/{gates,gates_test,merge,loop}.go`, `internal/config/{types,defaults,raw,render}.go` plus tests and testdata, `internal/state/migrations/0008_pr_watch.sql` (new), `internal/state/{events.go,prwatch.go (new)}` plus golden, `internal/prwatch/{doc,loop,watch}.go` and tests (new), `internal/servecmd/{serve.go,prwatch.go (new)}` plus tests, `CLAUDE.md`, `docs/proposal.md`.
