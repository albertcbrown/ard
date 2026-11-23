# Plan for #41

## Implementation

### Steps

This plan was rewritten by a maintainer PR. It folds in the owner-approved review of the first plan (the amendments in the issue body and the issue comment). Where the issue body and this plan differ, follow this plan.

Owner decisions:

- **Every requested reviewer reruns after a fix round**, not only the ones that asked for changes. Merge-gating approvals must cover the merged head (`prgate.Covers`, used by mark-ready and the merger), so an approval at the old head can never count. `maybeReady`, `internal/prgate` and `internal/merger` are not changed.
- **Round counts stay local** in `_controller.db` (the `events` table). No GitHub marker is needed to rebuild them.
- **Merger conflict and rebase rounds are a follow-up.** This PR only exposes the round API (step 3); it does not change the merger.
- **The prompts are done.** PR #206 already taught `.ard/workers/engineer/prompt.txt` to read `.ard-input/<n>/review-feedback.md` and `.ard/reviewers/eng-review/prompt.txt` to read `.ard-input/<n>/previous-review.md`. Produce exactly those two paths. Do not edit `CLAUDE.md` or anything under `.ard/` except your own report; the diff guard holds the job if you do.

Precondition: this builds on #207 (merged), which added `internal/prgate` (`ParseWorkerBranch`, `Blocked`, `HasLabel`, `HasAny`, `Covers`) and `closeout.ReviewPassed`. If either is missing from your base, stop and answer `needs-human` with "blocked: #207 (internal/prgate) is not on main".

Design: a new controller loop `reviewfix` owns the transition. Labels carry the live state; a new append-only event kind carries the round count. It needs no job, no pool slot and no agent. Every comparison between events uses `Event.ID` (ascending insertion order), never `Event.At` (stored as Unix seconds, so same-second events tie).

1. **Event kind.** `internal/state/events.go`: after `EventMerge` add `// EventReviewRound: a review fix round changed phase; data has round, phase, reason, pr, head, worker and roles.` and `EventReviewRound = "review_round"`. Rows use `Issue` = issue number, `Role` = worker role, `Stage` empty. No migration.

2. **Proof of changes.** `internal/closeout/closeout.go`, next to `ReviewPassed`: add `func ReviewChanged(ctx, s interface{ CloseoutsOf(...) }, issue, pr int, role string) (sha string, ok bool, err error)`. Same walk as `ReviewPassed` (newest first, same role and PR); ok only when the newest such record has `Phase == PhaseClosed`, `Plan.Outcome == OutcomeChanges` and a non-nil `Plan.Review`; sha is `Plan.Review.CommitSHA`. A `:changes` label without such a record does not count (a person can add labels by hand).

3. **Round API.** New package `internal/reviewfix/`: `doc.go` (package comment: what the loop does, the phases, the cap), `round.go`, `loop.go`.
   `round.go`:
   - `const (PhaseRequested = "requested"; PhaseApplied = "applied"; PhaseCapped = "capped"; ReasonReview = "review"; ReasonConflict = "conflict")`.
   - `type Round struct { ID int64; N int; Phase, Reason string; PR int; Head, Worker string; Roles []string }` (JSON tags `round, phase, reason, pr, head, worker, roles`; `ID` is the event ID, not stored in data).
   - `type EventStore interface { Events(ctx, state.EventFilter) ([]state.Event, error); AppendEvent(ctx, state.Event) error }`.
   - `func Rounds(ctx context.Context, s interface{ Events(...) }, issue int) ([]Round, error)`: `Events(Kind: EventReviewRound, Issue: issue)`, decoded in ID order. A row that fails to decode is an error (fail closed).
   - `func Requested(rs []Round) int`: the number of `requested` rounds (any reason). This is the count `max_review_rounds` caps, for the life of the issue.
   - `func (l *Loop) RequestRound(ctx context.Context, r Request) (started bool, err error)` with `type Request struct { Issue, PR int; Head, Worker, Reason string; Roles []string; Max int }`. Reads `Rounds`; if `Requested >= Max` it runs the cap path (step 4, "cap") and returns false. Otherwise it appends a `requested` event (`N = Requested+1`, the request's fields) FIRST, then `RemoveLabel(issue, ard:worker:<worker>:done)`. The worker's request label stays (close-out only swaps `:running` for `:done`), so the worker loop claims the issue on its next tick. This is the call the merger's conflict path will use in the follow-up.

4. **The loop.** `internal/reviewfix/loop.go`. `Name()` `"reviewfix"`; `Claims()` true (skipped while paused); `Interval` = `c.Intervals.Reviewers` (default 5m, as `reviewer.Loop.Interval`); `TickTimeout` 5m; `var _ controller.Loop = (*Loop)(nil)`. `Deps`: `Reader` (Identity, GetIssue, GetPR, SearchIssues, ListComments), `Effects` (AddLabels, RemoveLabel, CreateComment, DryRun), `Store` (Events, AppendEvent, RunningJobs, CloseoutsOf), `Git` (FetchRef, RevParse, CodeChanged), `RepoDir`, `RemoteURL`, `Token gitx.TokenFunc`, `Log`. Declare these interfaces in the package, as `internal/merger/loop.go` does.
   `Tick`: return nil when `env.Cfg` is nil or has no reviewer roles. In dry-run (`Effects.DryRun()`) log `reviewfix.would_request`, `reviewfix.would_apply` or `reviewfix.would_cap` instead of acting, and append no events. Candidate PRs, de-duplicated: (a) for each reviewer role, `SearchIssues(is:pr is:open label:"ard:review:<role>:changes", Max 50)`, as `reviewer.sweepReady` does; (b) the `PR` of every issue whose newest `review_round` event is `requested` (one `Events(Kind: EventReviewRound)` call, grouped by issue). Call `handle(pr)` for each; log and `errors.Join` per-PR errors, never stop at the first.
   `handle`:
   - **Guards.** `GetPR`: open, not merged, author equals `Identity().Login` (case-insensitive), base `main`, `prgate.ParseWorkerBranch(HeadRef)` ok, not `prgate.Blocked(pr.Labels)`. `GetIssue(issue)`: open, not a PR, and none of `ard:needs-human`, `ard:hold`, `ard:executor:failed`, `ard:tests:failed` (`prgate.HasAny`). A worker failure labels the issue, not the PR. The worker role must exist (`cfg.Role(config.KindWorker, role)`); its `MaxReviewRounds` is the cap. Use `prgate.HasLabel`/`HasAny` for every label test; add no new label helper.
   - **Quiet.** `requested` = reviewer roles of the config whose `ard:review:<r>` is on the PR. Wait (return nil) unless every one of them has `:done` or `:changes`, none has `:running`, and `RunningJobs` holds no job with stage `review` or `worker` for this issue.
   - **Newest event `requested` (R).** Look for a worker claim: `Events(Kind: EventClaim, Stage: "worker", Issue: n)` with `ID > R.ID`.
     - No such claim and the issue still has `ard:worker:<role>:done`: the label removal was lost. Replay `RemoveLabel(:done)`; stop.
     - No such claim otherwise, or the issue has `:running` or lacks `:done`: the worker is pending or running; stop.
     - Claim found, `:done` present, no `:running`: the fix landed. Fetch the PR branch (`FetchRef(+refs/heads/<HeadRef>:refs/ard/reviewfix/<pr>)`) and `RevParse` it; if it differs from `pr.HeadSHA`, stop (retry next tick). `covered, _, err := prgate.Covers(ctx, Git, RepoDir, R.Head, pr.HeadSHA, "", n)`. If covered (no code outside `.ard/<n>/` changed; the worker's own close-out commit always moves the head, so compare code, not SHAs), the worker pushed no fix: run the cap path with the "no code" text. Otherwise, for **every** role in `requested`, remove `ard:review:<r>:changes` and `ard:review:<r>:done` from the PR where present, and only then append an `applied` event (same `N`, `Head = pr.HeadSHA`). Labels before the event: a crash in between replays the removals, never starts a spurious round. The reviewer loop then re-claims every requested role at the new head.
   - **Otherwise (no event, newest `applied`, or newest `capped`).** `changed` = roles in `requested` with `:changes` on the PR and `closeout.ReviewChanged` ok; a role with the label but no record is logged `reviewfix.changes_unproven` and ignored. If `changed` is empty, stop. If the newest event is `capped` with `Head == pr.HeadSHA`, stop: a human owns it, and a person who removes `ard:needs-human` must not get it back on the same head. Otherwise call `RequestRound(Request{Issue: n, PR: pr.Number, Head: pr.HeadSHA, Worker: role, Reason: ReasonReview, Roles: changed, Max: MaxReviewRounds})`; log `reviewfix.request` (issue, pr, round, roles) or `reviewfix.cap_reached`.
   - **Cap path** (shared by the cap and the no-code case): `agentjob.PostOnce` on the issue with marker `<!-- ard:review-rounds head=<sha> -->` and a fixed text naming the cap (or "the last fix round changed no code") and the roles still asking for changes, never finding text; then `AddLabels(issue, ard:needs-human)`; then append a `capped` event (`Head = pr.HeadSHA`, `N` = current count). Comment and label before the event, so a crash replays them (PostOnce is idempotent).

5. **Wiring.** New `internal/servecmd/reviewfix.go`: `func reviewfixLoop(w *wiring) controller.Loop` returning `reviewfix.New(reviewfix.Deps{Reader: w.Reader, Effects: w.Effects, Store: w.Store, Git: gitx.New(""), RepoDir: w.Root, RemoteURL: w.RemoteURL, Token: w.Token, Log: w.Log})`, modelled on `servecmd/merger.go`. In `serve.go` `registerLoops`, append it right after the reviewer loop, and add `"reviewfix"` to `knownLoops` after `"reviewers"`. Update `internal/servecmd/loops_test.go` for the new name.

6. **The worker never runs beside a review.** `internal/worker/candidates.go`: the busy check becomes `j.Stage == stageWorker || j.Stage == "triage" || j.Stage == "review"`. The reviewer loop already skips an issue with a running worker job.

7. **Worker feedback input.** New file `internal/worker/feedback.go`: `func (l *Loop) reviewFeedback(ctx context.Context, role config.Role, n int, branch string) (executor.File, bool, error)`. Call it in `run` (`internal/worker/run.go`) right after `resolveBranch`, only when `exists`; on success append the file to `inputs` and its path to `untrusted`; on error log `worker.claim_failed` with `step=review_feedback` and return, like the other claim-time reads.
   - It applies only when the newest round from `reviewfix.Rounds(ctx, l.d.Store, n)` has phase `requested`; otherwise return false (a first run, or a rerun with no round, gets nothing).
   - Path: `agentjob.InputDir(n) + "/review-feedback.md"`. Content: `agentjob.UntrustedHeader`, then `Review round <N> of <role.MaxReviewRounds>; reviewers asking for changes: <roles>.`, then for each role in the round's `Roles` a `### review-<role>.md` heading and the committed role file, read with `l.d.Git.FileAt(ctx, l.d.RepoDir, localRef(branch), ".ard/<n>/review-<role>.md", 64<<10)` (the branch was just fetched to `localRef`; `FileAt` rejects symlinks and non-regular files; a missing file is skipped). These files are redacted and structured (file, line, severity, blocking, message).
   - Then a `### Pull request comments` section: find the PR with `ListPRs(State: open, Head: branch)` (as `run.go` does at the PR step), take `ListComments(pr)` and `ListReviewComments(pr)`, drop every comment whose author equals `Identity().Login` (the App's review renderings duplicate the role files), and render each as `<login> at <RFC3339>` plus, for inline comments, `<path>:<line>`, then the body. Do not call `ListReviews` or parse review headers.
   - Cap the file at 64 KiB: keep the head and append `\n[truncated]\n`.
   - Add `FileAt(ctx, dir, rev, path string, maxBytes int64) ([]byte, bool, error)` to the worker `Git` interface, `ListReviewComments` to its `Reader`, and `Events` to its `Store` (all three are already methods of `*gitx.Git`, the GitHub client and `*state.Store`; check `internal/worker/harness_test.go` compiles).
   - In `triageInputs` (`run.go`), also list `.ard/<n>/` and `.ard/<n>/logs/` entries with prefix `review-` (same `list` helper, same `triageInputCap`), so the reviewer role files and logs on the branch are passed as untrusted paths.

8. **Reviewer previous findings.** `internal/reviewer/loop.go`: add `FileAt` (same signature) to the reviewer `Git` interface. `internal/reviewer/run.go`, after the inputs are built: `b, ok, err := d.Git.FileAt(ctx, d.RepoDir, p.tip, ".ard/<n>/review-<role>.md", 64<<10)`. The pinned head contains the reviewer's earlier close-out commit (it is committed after the head it reviewed), so read at `p.tip`, never at the head of the earlier claim. When ok, add input `dir + "/previous-review.md"` = `agentjob.UntrustedHeader` + `"Your report from the previous review round on this pull request:\n\n"` + b, and append the path to `untrusted`. No event logic: provide it whenever it exists. On error log `review.previous_review_failed` at Warn and continue without it. The executor still scrubs the stale `review-<role>.md` from the worktree.

9. **Docs.** `docs/proposal.md`:
   - "Review feedback loops back here" (Worker loop): replace with the `reviewfix` loop: it starts a round when every requested reviewer has finished and at least one ended `:changes` backed by its close-out record; it records a `review_round` event (counted at request, ordered by event ID, local to `_controller.db`) and removes `ard:worker:<role>:done`; the worker fixes on the same branch with `review-feedback.md`; when the fix lands with code changes, every requested reviewer reruns at the new head (removing their `:done`/`:changes`), because merge-gating approvals must cover the merged head; a round with no code change, or reaching `max_review_rounds` (default 3), posts one comment and adds `ard:needs-human` on the issue, and is not repeated on the same head after a person removes it. Say merger conflict rounds will use the same round API (follow-up).
   - Worker step 4 and the starter-worker paragraph: replace "Review feedback is added by the review-feedback loop" with `.ard-input/<issue#>/review-feedback.md` in a review round, and the reviewer role files and logs among the untrusted inputs.
   - Reviewer loop step 3: in a later round the reviewer gets `.ard-input/<issue#>/previous-review.md`, read from the pinned head.
   - Merger paragraph ("re-requests the worker for a rebase round"): mark it as a follow-up that uses the round API.
   Do not edit `CLAUDE.md`; put this Layout line in the PR body for a maintainer: `internal/reviewfix/  review fix rounds: the review_round event, Rounds/RequestRound, and the loop that sends :changes PRs back to the worker and reruns reviewers`.

### Test plan

Run `go test ./internal/reviewfix/ ./internal/closeout/ ./internal/worker/ ./internal/reviewer/ ./internal/servecmd/ ./internal/state/`, then `make lint test`. Use `internal/github/fake` and a real temp `state.Store`, as `internal/merger/harness_test.go` does. Keep tests table-driven and compact (see Size).

`internal/reviewfix/loop_test.go` (with a small `harness_test.go`: fake GitHub with an App identity, a worker PR on `worker/engineer-<n>-x`, temp store, a stub `Git` whose `CodeChanged` returns a configurable path list, a frozen clock). One table `TestHandle`, each row seeding labels, events, close-out records and running jobs, running one tick and asserting events, labels and comments:
- changes with a `changes` close-out record: one `requested` event (round 1, roles, head), issue `:done` removed, request label kept.
- `:changes` label without a record: no event, `reviewfix.changes_unproven` logged.
- another requested reviewer `:running`, or still pending (request only), or a running `review` job: no write.
- lost removal: `requested` event, no later worker claim, issue still `:done`: label removed, no second event.
- worker claim with the **same timestamp** as the `requested` event but a higher ID (frozen clock): treated as landed, not as a lost removal.
- worker running (`:running`, or claim without `:done`): no write.
- fix landed with code changed: `:changes` and `:done` removed for every requested role (also an approver's `:done`), then one `applied` event.
- fix landed with no code change (`CodeChanged` empty): one comment, `ard:needs-human`, `capped` event, no label removal.
- cap: `max_review_rounds: 2` and two `requested` events: comment with the `head=<sha>` marker, `ard:needs-human`, `capped` event, no third `requested`.
- cap cleared by a person: newest `capped` at the current head, `ard:needs-human` removed: no label, no comment.
- skips: non-App author, base not `main`, non-worker branch, PR `ard:hold`, issue `ard:tests:failed`, issue `ard:executor:failed`, closed issue: no write.
- dry-run: no events, no label or comment calls.
- two ticks with no outside change: still one `requested` event.
- `RemoveLabel` failing on one PR: `Tick` returns an error and the other PR is still handled.

`internal/servecmd/reviewfix_e2e_test.go` (the issue's "Done when"): reuse `newReviewE2E` with a scripted reviewer that answers `changes` while `hello.go` returns `"hi"` and `approve` once it uses its argument, add a worker role `engineer` to `e.cfg`, and build the `reviewfix` loop on the same wiring. Simulate the worker (do not wire the worker loop, to stay in budget): a helper commits a fix to the branch in the clone, pushes it, updates the fake PR's `HeadSHA`, records a worker job with `w.Store.CreateJob` + `Complete`, and puts `ard:worker:engineer:done` back on the issue.
- `TestReviewFixRoundEndToEnd`: review → `:changes`; reviewfix → round 1 requested; worker helper; reviewfix → applied, labels cleared; review → approve, `:done`; the PR is marked ready (no longer draft); the round count is 1.
- `TestReviewFixCapEndToEnd`: `max_review_rounds: 1`, the reviewer always asks for changes: after one round the next changes verdict posts exactly one comment and adds `ard:needs-human`; a further reviewfix tick posts nothing.

Other files:
- `internal/worker/candidates_test.go`: a running `review` job makes the issue busy.
- `internal/worker/loop_test.go`: `TestReviewFeedbackInput`: a `requested` round, a committed `review-eng-review.md` on the branch, a human PR comment and an App inline comment: `review-feedback.md` is in the inputs and untrusted list, holds the round, the role file and the human comment, not the App's; with no round event it is absent.
- `internal/reviewer/run_test.go`: `TestPreviousReviewInput`: the branch's last commit adds `review-<role>.md` after the reviewed code commit and the PR head is that commit: `previous-review.md` holds it; a first review gets none.
- `internal/servecmd/loops_test.go`: `reviewfix` is registered and in `knownLoops`.

### Risk

Medium.
- A new loop writes labels; a bug could loop worker runs. The cap counted at request, event-first ordering, ID ordering, the quiet wait and the no-code cap contain it, and the idempotence rows test it.
- Every requested reviewer reruns each round, which costs more review jobs than the issue's first scope line ("only reviewers that asked for changes"); the owner chose this so the merger's `prgate.Covers` check holds. No merge-gating rule is loosened.
- Untrusted text (role files, PR comments) reaches agents only as marked input files and is never committed or posted. The cap comment quotes no finding.
- Round counts are lost with `_controller.db`; at most `max_review_rounds` extra rounds per loss (accepted by the owner).
- Protected paths: none changed. `CLAUDE.md` needs a Layout line a maintainer adds from the PR body.

### Size

L, about 800 to 900 changed lines; the worker diff guard holds the PR above 1,000 (whole PR, all rounds). Budget: production about 430 (reviewfix about 300, worker feedback about 80, reviewer about 20, closeout about 25, wiring and state about 20), tests about 400 (reviewfix table about 200, e2e about 120, worker, reviewer and candidates about 80), docs about 15. Keep the tests table-driven and reuse the existing harnesses. If the projection passes 950 lines, keep steps 1 to 6 and 8, the reviewfix table and the e2e test, and answer `needs-human` naming what is left rather than going over.

Files: `internal/state/events.go`, `internal/closeout/closeout.go`, `internal/reviewfix/{doc,round,loop,harness_test,loop_test}.go` (new), `internal/servecmd/{reviewfix.go,serve.go,loops_test.go,reviewfix_e2e_test.go}`, `internal/worker/{candidates.go,candidates_test.go,feedback.go,run.go,loop.go,loop_test.go}`, `internal/reviewer/{loop.go,run.go,run_test.go}`, `docs/proposal.md`.
