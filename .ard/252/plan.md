# Plan for #252

## Implementation

### Steps
1. `internal/state/events.go`: add `EventReviewRerequest = "review_rerequest"` (new) to the event kinds, with a comment: the merger removed a stale `:done` review label; data has `pr`, `head`, `reviewed`; stage `review` and role in their columns. Reason: once-per-head record. No schema change (events table is generic).
2. `internal/merger/merge.go`, in `consider` step e: replace the two `merge.reviews_stale` hints. The "no passing close-out record" case keeps its log, with the wrong hint replaced (for example "the review has not passed on this PR; it re-runs when the role label is requested"). It does not re-request. In the `!ok` branch of `prgate.Covers` (code changed since the reviewed commit; main-only moves already give `ok == true`), keep the `merge.reviews_stale` log with a corrected hint ("the merger re-requests this review once per head") and call new `t.rerequestReview(ctx, pr, issue, role, reviewed, head)` before `return false`.
3. `internal/merger/merge.go` (new method):
   `func (t *tick) rerequestReview(ctx context.Context, pr github.PullRequest, issue int, role, reviewed, head string)`.
   - Skip if `labels.Done(labels.StageReview, role)` is not in `pr.Labels` (use `prgate.HasLabel`). Nothing to remove then.
   - Read `d.Store.Events(ctx, state.EventFilter{Kind: state.EventReviewRerequest, Issue: issue, Stage: "review", Role: role})` and return if any event's data has `pr == n && head == head` (once per head). On a read error call `t.fail(n, "rerequest_events", err)` and return, which fails closed.
   - Call `d.Effects.RemoveLabel(ctx, n, labels.Done(labels.StageReview, role))`. On error, `t.fail(n, "rerequest_label", err)` and return without recording the event, so the next tick retries.
   - If `t.dry`: log `merge.would_rerequest` and do not append the event. The ledger is the only write, like `merge.would_update`. Otherwise log `merge.review_rerequested` (pr, issue, role, reviewed, head) and append the event with `Stage: "review"` and `Role: role` via `d.Store.AppendEvent`. A failed append is a warn `merge.event_failed`, matching the existing merge event.
   - The kill switch needs no code: `Loop.Claims` returns true, so the controller skips the merger while paused.
   - Whether the reviewer actually re-runs once `:done` is gone, given `ard:review` request labels and the claim rules, must be confirmed by the worker in `internal/reviewer` (loop and claim code). If the role also needs `ard:review:<role>` re-added, add it with `d.Effects.AddLabels` in the same method.
4. `internal/reviewer/ready.go` (optional, only if the mark-ready sweep also needs it): the issue allows either place. The merger alone is enough, so leave the reviewer unchanged.
5. `docs/proposal.md`, Merger loop section, first bullet: replace "A stale review is only logged: a person must re-request it; fix rounds rerun reviewers only after a worker fix" with a description of the once-per-head re-request (removes `ard:review:<role>:done`, records a `review_rerequest` event, logs `merge.review_rerequested`, skips a main-only move, dry-run logs `merge.would_rerequest` and writes only to the ledger). Update the Pause and dry-run paragraph to name it. Also add the event kind where the proposal lists events, if it does.
6. `internal/merger/doc.go`: mention the re-request next to the other logged outcomes, if it lists them.

### Test plan
Test command: `make test` (`go test ./...`), plus `make lint`. Add cases to `internal/merger/loop_test.go`, using the harness in `internal/merger/harness_test.go` and the existing stale-review test as the model.
- `TestStaleReviewRerequestedOncePerHead`: a done review at commit A, head B with a code-path change. Tick 1 removes `ard:review:<role>:done` and appends one `review_rerequest` event. Tick 2 with the same head and the label put back does not remove it again and does not append. Expect exactly one RemoveLabel call. A later head C with a new code change gives a second re-request.
- `TestMainOnlyMoveNotRerequested`: the head moved by an update-branch merge from main only. `Covers` is true, no RemoveLabel, no event.
- `TestStaleReviewDryRunWritesOnlyLedger`: with `effects.DryRun`, the ledger holds the `remove_label` record, the store has no `review_rerequest` event, and the log has `merge.would_rerequest`.
- `TestRerequestSkippedWithoutDoneLabel`: the stale case where the `:done` label is absent does nothing.
- `TestRerequestLabelErrorRetries`: RemoveLabel fails, so no event is written and the next tick retries.
- `TestNoPassingRecordNotRerequested`: the no-close-out-record case only logs, with the new hint.
- `TestReviewerPausedNoRerequest`: assert the merger loop is skipped while paused, if the existing tests do not already cover `Claims`.
- Check the log hint no longer says "fix rounds (#41) will do this".

### Risk
Low to medium. The merger is controller code that removes a label (an outward write), so the once-per-head guard and the fail-closed event read are what prevent a loop. A reviewer that does not re-claim after the label removal would leave the PR stuck, as it is today, so confirm the claim path. The merge gates are unchanged. No protected path (`merge.protected_paths`) is touched; `docs/` and `internal/` are not protected. No migration and no public interface change beyond one new event-kind constant. No concurrency change: it runs inside the single merger tick.

### Size
M. Files: `internal/state/events.go`, `internal/merger/merge.go`, `internal/merger/loop_test.go` (and `harness_test.go` if a helper is needed), `docs/proposal.md`, optionally `internal/merger/doc.go`.
