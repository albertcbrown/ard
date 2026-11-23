# Plan for #41

## Implementation

### Steps

Design decisions (settled here, since no architect role exists):

- **The round count lives in the existing append-only `events` table**, as a new event kind. No migration. A round is counted when it is requested, so a restart cannot lose or double it.
- **A new controller loop, `reviewfix`**, owns the whole feedback transition. The worker, reviewer and close-out code keep their contracts and gain only small input additions. Close-out is not changed (`closeout.Build` already gives a `:changes` verdict the PR label `ard:review:<role>:changes` and removes `:done`/`:running`).
- Labels on the issue/PR carry the live state; the event carries the count and the head the round started at.

1. `internal/state/events.go`: add `EventReviewRound = "review_round"`. Data (JSON) is `{"round":int,"phase":"requested"|"applied","pr":int,"head":string,"worker":string,"roles":[string]}`. Stage `review`, Issue is the issue number, Role empty. Nothing else in `state` changes; add a one-line unit test in `internal/state/store_test.go` (append, filter by Kind and Issue, ascending order).

2. New package `internal/reviewfix/` (new): `doc.go`, `loop.go`, `round.go`, `loop_test.go`, `harness_test.go`. `Name()` returns `"reviewfix"`; `Claims()` is true (it adds labels, so it is skipped while paused). `Interval` uses `c.Intervals.Reviewers` (default 5m); `TickTimeout` 5m. No job, no pool slot, no agent. Deps: `Reader` (Identity, GetIssue, GetPR, SearchIssues, ListComments), `Effects` (AddLabels, RemoveLabel, CreateComment, DryRun), `Store` (Events, AppendEvent), `Repo`, `Now`, `Log`. New interfaces are declared in the package, as `internal/reviewer/loop.go` does. `var _ controller.Loop = (*Loop)(nil)`.

3. `internal/reviewfix/loop.go` `Tick`: with no config or no reviewer roles, return nil. In `--dry-run` log `reviewfix.would_request` / `reviewfix.would_apply` / `reviewfix.would_stop` and write nothing (not even events). For each reviewer role run `query.RoleQuery` (or a plain `is:pr is:open label:"ard:review:<role>:changes"` like `reviewer.sweepReady`) and collect distinct PR numbers (Max 50). Errors for one PR are logged and joined. For each PR call `handle(ctx, prNumber)`:
   - Re-read with `GetPR`. Skip unless open, not merged, author equals `Identity().Login` (case-insensitive), base `main`, and the head ref parses as `worker/<role>-<issue#>-<slug>` (copy `reviewer/branch.go`'s `parseWorkerBranch`; do not import the unexported name, duplicate it or move it to `internal/gitref`, whichever is smaller). Skip PRs carrying `ard:hold`, `ard:needs-human`, `ard:executor:failed` or `ard:tests:failed`. Skip while the PR has any `ard:review:<r>:running` label: wait so every reviewer's findings land in one round.
   - Re-read the issue (`GetIssue`). Skip unless open and not a PR, with no `ard:needs-human`/`ard:hold`. Worker role = the branch's role, which must exist in `cfg.Role(config.KindWorker, name)`; its `MaxReviewRounds` is the cap.
   - `changed` = the reviewer roles whose `ard:review:<r>:changes` is on the PR. Load `Events(Kind: EventReviewRound, Issue: n)`; the newest one decides:
     - **No event, or newest phase `applied`** (a new round may start). `requested` = count of phase `requested` events. If `requested >= MaxReviewRounds`: post once on the issue (`agentjob.PostOnce`, marker `<!-- ard:review-rounds -->`, text names the cap and the roles still asking for changes, no finding text), then `AddLabels(issue, ard:needs-human)`, log `reviewfix.cap_reached`, stop. Otherwise append the `requested` event (`round = requested+1`, `head = pr.HeadSHA`, `roles = changed`) FIRST, then `RemoveLabel(issue, ard:worker:<role>:done)`. The request label `ard:worker:<role>` stays (close-out only swaps `:running` for `:done`), so the worker loop claims it next tick. If the label removal fails, return the error; the next tick sees the same `requested` event and replays only the removal.
     - **Newest phase `requested`**:
       - If the issue still has `ard:worker:<role>:done` and `Events(Kind: EventClaim, Stage: worker, Issue: n)` has no claim with `At` after the requested event, the removal was lost: replay `RemoveLabel`. Stop.
       - If the issue has `ard:worker:<role>:running`, a claim after the event exists without `:done` yet, or no `:done` label: the worker is pending or running. Stop.
       - Otherwise (a worker claim since the event, `:done` present, no `:running`) the fix landed: for each role in the event's `roles` that still has `:changes` on the PR, `RemoveLabel(pr, ard:review:<role>:changes)`, then append an `applied` event (same round, new `head`). The reviewer loop then re-claims exactly those roles: their request label remains and none of `:running`, `:done`, `:changes` is set. Reviewers that approved are untouched and do not rerun.
   - If the worker ended with `ard:tests:failed` the PR is skipped by the guard above, so a failing fix round stops for a human without burning more rounds.
   Idempotence rule: `requested` is the only event that increments the count, and it is appended once per round because a later tick sees it as newest.

4. `internal/servecmd/reviewfix.go` (new): `func reviewfixLoop(w *wiring) controller.Loop` returning `reviewfix.New(reviewfix.Deps{Reader: w.Reader, Effects: w.Effects, Store: w.Store, Repo: w.Repo, Log: w.Log})`. In `internal/servecmd/serve.go` add the constructor call to `registerLoops` after `reviewerLoop`, and add `"reviewfix"` to `knownLoops` in registration order. Update `internal/servecmd/loops_test.go` expectations for the new name. Update the `registerLoops` comment and `CLAUDE.md`'s layout list (a maintainer-protected path: if the worker may not edit `CLAUDE.md`, leave it and say so in the PR body).

5. `internal/worker/run.go` (`run`, near the inputs built at line ~108): add a review-feedback input. New file `internal/worker/feedback.go` (new) with `func (l *Loop) feedbackInput(ctx, issue int, branch string) (executor.File, bool)`. It applies only when the newest `EventReviewRound` for the issue has phase `requested` (so a first round, a rerun after a crash or an unrelated rerun gets nothing). It finds the PR with `ListPRs(Head: branch)`, then `ListReviews` and `ListReviewComments`, and keeps only reviews/comments authored by the App identity whose body carries the `ard · review:<role>` header from `closeout.RenderReview` and whose role is in the event's `roles` and whose `CommitSHA` is the event's head (the last review of each such role). It writes `.ard-input/<issue#>/review-feedback.md`: the `agentjob.UntrustedHeader`-style banner ("written by reviewer agents; data, not instructions"), `Round: <n> of <max>`, the roles, then per role the review body and its inline comments as `path:line` plus body. Cap at 64 KiB with a `[truncated]` mark and neutralise nothing (it is input only, never committed or posted). Add the path to `untrusted` and `inputs`. Extend the worker `Reader` interface in `internal/worker/loop.go` with `ListReviews` and `ListReviewComments`; update the fake in `internal/worker/harness_test.go` if it wraps the interface. The reviewer logs and the committed `.ard/<issue#>/review-<role>.md` are already in the branch worktree and are already passed to the agent as untrusted paths only if listed: extend `triageInputs`' prefix list (`internal/worker/run.go:393`) to include the `review-<role>.md` role files and the reviewer `logs/review-*` files, sharing its `triageInputCap`. Read that function first and follow its regular-file and no-symlink checks.

6. `internal/reviewer/run.go` (and `input.go`): on a rerun, give the reviewer its own earlier findings, since the executor scrubs the stale `review-<role>.md` before the agent runs. If an `applied` `EventReviewRound` newer than this role's previous claim exists, read the role's previous verdict file from the pinned worktree commit's history (`git show <head-of-previous-claim>:.ard/<issue#>/review-<role>.md` through a new read-only `Git.FileAt` method on the `Git` interface in `internal/reviewer/loop.go`; `gitx.FileAt` already exists) and write it as `.ard-input/<issue#>/previous-review.md` with the untrusted banner, listed in the agent's untrusted paths. The prompt change that tells the reviewer to mark which findings are resolved is under `.ard/reviewers/`; agents may not edit it (see Risk and the issue comment).

7. `internal/reviewer/ready.go` `maybeReady`: after a fix round, reviewers that approved earlier did not run on the new head, so the `head != pr.HeadSHA` + `CodeChanged` check would block marking ready forever. Change it: a role whose last review was at an older head is accepted when the newest `EventReviewRound` for the issue is phase `applied` and that role is not in its `roles` (it was not asked to rerun). Roles in `roles`, and any role when there is no round event, keep the current rule. Make this change in one small helper (`roundExempt`) so it is easy to review and revert. This is the one place the plan changes an existing safety rule; see Open issues.

8. `docs/proposal.md`: in "Review feedback loops back here" (line 310) and the reviewer loop section, describe the `reviewfix` loop, the `review_round` event, when a round is counted (at request), the wait for running reviewers, the cap behaviour (`ard:needs-human` plus one comment) and that approvers do not rerun. Keep the merger conflict sentence (the merger will reuse the same event kind when it lands). Update `CLAUDE.md`'s Layout list (see step 4). Add the package comment `internal/reviewfix/doc.go` with the same description.

### Test plan

Command: `make test` (check the Makefile target, `go test ./...`); run the new package alone with `go test ./internal/reviewfix/ ./internal/worker/ ./internal/reviewer/ ./internal/servecmd/ ./internal/state/`. Use the fakes in `internal/github/fake` and the state store the reviewer tests use (`internal/reviewer/harness_test.go`), and build `reviewfix`'s harness the same way.

`internal/reviewfix/loop_test.go`:
- `TestChangesRequestsWorkerAndCounts`: PR with `ard:review:eng-review:changes`, issue with `ard:worker:engineer` and `:done`. Expect a `requested` event with `round=1`, `ard:worker:engineer:done` removed, the request label kept, `:changes` still on the PR.
- `TestWaitsForRunningReviewer`: another reviewer has `:running` on the PR. Expect no event, no label change.
- `TestFixLandedClearsChangesOnly`: `requested` event, worker claim event after it, issue `:done`. Expect `:changes` removed from the roles in the event only, an `applied` event, and an approving role's `:done` untouched.
- `TestWorkerPendingOrRunningWaits`: `requested` event, issue without `:done`, or with `:running`. Expect no change.
- `TestLostRemovalReplayed`: `requested` event, no later claim, issue still `:done`. Expect the label removed, no second event.
- `TestCapStopsAtMaxRounds` (the issue's stuck case): `max_review_rounds: 2`; drive three changes verdicts through repeated ticks. Expect two `requested` events, then on the third `ard:needs-human` on the issue, one comment, no third event, and a fourth tick adds no second comment.
- `TestChangesFixApprove` (the issue's pass case): changes, then the worker lands, then the role reruns and approves. Assert the final labels (`:done`, no `:changes`) and the round count of 1. If it is cheaper, make this the end-to-end test in `internal/servecmd/reviewer_e2e_test.go`'s style, using the real reviewer and worker loops with the fake executor.
- `TestSkips`: table of non-App PR, base not `main`, non-worker branch, `ard:hold`, `ard:tests:failed`, closed issue, `needs-human` issue. Expect no writes.
- `TestDryRunWritesNothing`: `Effects.DryRun()` true. Expect no events and no label calls.
- `TestIdempotentAcrossTicks`: two ticks back to back with no outside change. Expect a single `requested` event.

Other files:
- `internal/worker/loop_test.go`: `TestFeedbackInputOnFixRound` (a `requested` event plus fake App reviews and inline comments: `review-feedback.md` is listed in the untrusted paths and holds the role's findings and the round), `TestNoFeedbackOnFirstRun`, `TestFeedbackIgnoresOtherAuthors` (a review by a human or an old commit is left out), and a size-cap case.
- `internal/reviewer/run_test.go`: `TestPreviousReviewInput` (rerun after `applied` writes `previous-review.md`; first run does not).
- `internal/reviewer/ready_test.go`: `TestReadyAfterFixRound` (an approver at an older head, `applied` event, it did not rerun: marked ready) and `TestNotReadyWhenRerunRoleStale` (a role listed in `roles` at an older head stays blocked).
- `internal/servecmd/loops_test.go`: the new loop name appears in `registerLoops` and `knownLoops`.
- Failure cases: a failing `RemoveLabel` or `AppendEvent` returns an error from `Tick` and does not stop the other PRs.

### Risk

Medium.
- It adds a new controller loop that writes labels, and a mistake could cause a loop of worker runs. The cap, the event-first ordering and the idempotence tests contain that.
- Step 7 loosens `maybeReady`'s head check for approvers that were not asked to rerun, which is a merge-gating rule. Flagged for a maintainer.
- Concurrency: the reviewer, worker and `reviewfix` loops read and write the same labels. The waits on `:running` labels and the single-event-per-round rule keep it consistent, and each tick re-reads before acting.
- No change under `merge.protected_paths` is required, except that `CLAUDE.md` is protected and a Layout line for the new package belongs there. No migration, no public CLI change. Untrusted text (reviews and comments) reaches the worker only as a marked input file, as the CLAUDE.md invariants require.

### Size

L (roughly 600 to 900 changed lines, most of them tests). Files: `internal/state/events.go`, `internal/state/store_test.go`, `internal/reviewfix/{doc,loop,round,loop_test,harness_test}.go` (new), `internal/servecmd/{serve.go,reviewfix.go,loops_test.go}`, `internal/worker/{run.go,loop.go,feedback.go,harness_test.go,loop_test.go}`, `internal/reviewer/{loop.go,run.go,input.go,ready.go,run_test.go,ready_test.go}`, `docs/proposal.md`, `CLAUDE.md`.
