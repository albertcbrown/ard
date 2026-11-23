# Plan for #194

## Implementation

### Steps

1. `internal/reviewer/loop.go`: add `MarkReady(ctx context.Context, n int) error` to the `Effects` interface (already on `effects.Effects`; `Live.MarkReady` and `DryRun.MarkReady` exist, the latter writes a ledger line only). Add `Events(ctx context.Context, f state.EventFilter) ([]state.Event, error)` to `Store` (exists on `*state.Store`). Update the fakes in `internal/reviewer/harness_test.go` accordingly.
2. New `internal/reviewer/ready.go`:
   - `func (t *tick) maybeReady(ctx context.Context, prNumber int)`: re-read with `Reader.GetPR`; return unless open, not merged, `Draft`, author equals `t.self` (case-insensitive), `BaseRef == "main"` and `parseWorkerBranch(HeadRef)` ok. Skip if the PR has `labels.NeedsHuman`, `labels.Hold`, `labels.TestsFailed` or `labels.ExecutorFailed`.
   - Requested roles = every `labels.Request(labels.StageReview, role)` label on the PR (use `cfg.RolesOf(config.KindReviewer)`). Require at least one. For each: `labels.Done(labels.StageReview, role)` present and neither `labels.Changes(role)` nor `labels.Running(labels.StageReview, role)`.
   - Head check: for each role take the newest claim event (`state.EventClaim`, filtered by issue, stage `review`, role; its Data holds `{"head": ...}`, written in `run.go` step 2). Passed at the current head when that head equals `pr.HeadSHA`, or `Git.CodeChanged(RepoDir, reviewedHead, pr.HeadSHA, issueDir(n))` is empty after a `FetchRef` like `headMoved` in `run.go`. If the head cannot be determined, do not mark (fail closed) and log at debug.
   - If all hold: `Effects.MarkReady(ctx, pr)`; on error `log.Warn("review.mark_ready_failed", ...)` and return (retried next tick); on success `log.Info("review.marked_ready", "pr", pr, "issue", n, "head", pr.HeadSHA)`. Dry-run: the effects layer writes the ledger line; still log `review.marked_ready` only when not dry, otherwise `review.would_mark_ready`.
3. `internal/reviewer/ready.go`: `func (t *tick) sweepReady(ctx context.Context) []error`: `Reader.SearchIssues` for open draft PRs from the App with the `ard:review:<role>` request labels (use `query.RoleQuery`/a plain `is:pr is:open draft:true author:@me` query, `Max: 50`) and call `maybeReady` for each hit whose head ref is `worker/`. Per-PR errors are logged, never returned as a tick failure.
4. `internal/reviewer/loop.go` `Tick`: call `t.sweepReady(ctx)` before candidate selection and also on the early dry-run path (before the `return`), but after the identity lookup. It must run even when there are no candidates and when no model credential is set (the sweep needs no agent); move the credential early-return accordingly, only for the sweep call.
5. `internal/reviewer/run.go` `run`: after `finish(...)` at the end (and only when the outcome is a done verdict), call `maybeReady` on a `tick`-like helper built from `l`, `cfg`, `log`. Errors are logged inside `maybeReady`, so close-out is never failed. Build a small `tick{l: l, cfg: cfg, log: log, self: ..., dry: ...}`; `self` can come from `Reader.Identity` (cache it in `Loop`), or pass `c.pr.Author`.
6. `internal/reviewer/doc.go`: add a "Marking ready" section.
7. `docs/proposal.md`: in the worker section (around the line "Once tests pass, the controller marks the PR ready for review") and the reviewer close-out step (around "The reviewer never marks the PR ready and never edits it; the merger does"), replace with: a worker PR stays a draft until every requested review passes at its current head, then the controller marks it ready for review; the merger (Phase 2) merges ready PRs that also pass its gates. Also the line about "The merger takes it from here" in the worker section.
8. `docs/runbooks/gate-2.md` step 6 (lines ~80-84): replace "mark it ready, then Squash and merge" with: the controller marks the PR ready once every requested review is `:done` at the head; just **Squash and merge** (mark it ready by hand only if the reviews are still `:changes` and you are overriding). Also fix the comment in `internal/worker/run.go` above `openPR` ("marking it ready is the merger's").

### Test plan

Command: `make test` (`go test ./...`); also `make lint`. New tests in `internal/reviewer/ready_test.go`, using the harness in `harness_test.go` and `github/fake`.
- `TestMarksReadyWhenLastReviewDone`: two requested roles, both `:done` at the current head, draft App PR on `worker/…`: `MarkReady` is called once, `review.marked_ready` is logged with pr, issue and head.
- `TestNoMarkWhileBlocked` (table): one role `:changes`; one `:running`; one role still unfinished; `ard:executor:failed`; PR `ard:needs-human`, `ard:hold` or `ard:tests:failed`; no requested role. Expected: no `MarkReady`.
- `TestNoMarkWhenHeadMoved`: `CodeChanged` returns a file between the reviewed head and the current head: not marked. Head moved by `.ard/` files only: marked. Git error: not marked.
- `TestLeavesAloneAlreadyReadyClosedForeign`: PR already ready, closed or merged, authored by a human, base not `main`, branch not `worker/`: untouched.
- `TestDryRunOnlyLedger`: dry-run effects write one ledger line and the fake PR stays a draft.
- `TestSweepCatchesMissedMark`: the last review finished before a restart (labels in place, no run in flight); a plain `Tick` marks the PR.
- `TestMarkReadyFailureDoesNotFailCloseout`: fake injects an error on `MarkReady`; `run` still returns normally after `FinishWith`, `review.mark_ready_failed` is logged, and the next tick retries and succeeds.
- `TestNeverMarksTwice`: second tick after success does nothing.
- Extend `internal/servecmd/reviewer_e2e_test.go` for one end-to-end case.

### Risk

Medium. It changes when PRs leave draft, which affects the human merge workflow, and relies on head tracking from the claim event, so a wrong head comparison could mark a PR ready that was not reviewed at its head (hence fail closed). No protected paths are touched (`.github/`, `.ard/config.yaml`, role directories are untouched); no migration and no new public interface beyond the two small interface additions in `internal/reviewer`. It never merges and never returns a PR to draft. The sweep adds one search and a few reads per tick; the code runs on the loop goroutine and in `Inflight` watchers, so keep `maybeReady` free of shared state.

### Size

M. Files: `internal/reviewer/loop.go`, `internal/reviewer/run.go`, `internal/reviewer/ready.go` (new), `internal/reviewer/doc.go`, `internal/reviewer/harness_test.go`, `internal/reviewer/ready_test.go` (new), `internal/servecmd/reviewer_e2e_test.go`, `internal/worker/run.go` (comment), `docs/proposal.md`, `docs/runbooks/gate-2.md`.
