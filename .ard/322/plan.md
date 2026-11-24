# Plan for #322

## Implementation

### Steps

Paths are under `internal/`. "New" marks anything that does not exist yet. Nothing under `.ard/` and no config key is touched. The switch is that `servecmd/merger.go` leaves `merger.Deps.Rounds` nil, so production keeps `needs-human` on conflict until part B wires it.

1. `reviewfix/round.go`, `reviewfix/loop.go`: add a `reason string` parameter to `capPath(ctx, issue, pr, head, worker, count, roles, why, reason)`. Write it into the capped `Round` instead of the hard-coded `ReasonReview` (loop.go:381). For `ReasonConflict` word the cap comment "Conflict rounds stopped on pull request #%d: %s. ..." and keep the marker `<!-- ard:review-rounds head=... -->` and the review wording unchanged for `ReasonReview`. Callers: `RequestRound` (round.go:114) passes `r.Reason`; `landing` (loop.go:345) passes the round's `R.Reason`. The count already includes every requested round, so conflict and review rounds share `max_review_rounds` through `Requested(rs)`. Reason: the cap comment and event must say why the round ran.
2. `reviewfix/loop.go` `Git` interface and `gitx/inspect.go`: add `(g *Git) IsAncestor(ctx, repoDir, a, b string) (bool, error)` (new) running `git merge-base --is-ancestor a b` (exit 1 means false, not an error). Add it to `reviewfix.Git`. Check the reviewfix test fakes and add the method there.
3. `reviewfix/loop.go` `landing` (after the tip check, before `prgate.Covers`): when `R.Reason == ReasonConflict`, compute `anc, err := d.Git.IsAncestor(ctx, d.RepoDir, R.Head, pr.HeadSHA)`. Applied means `anc && pr.HeadSHA != R.Head` and the head is a merge (use a new `ParentCount` via `RevParse(ref+"^2")` or add `gitx` `IsMerge`; prefer a small `Parents(ctx, dir, rev) ([]string, error)` in gitx, new, and require `len>=2` with `R.Head` reachable). Applied skips `Covers` entirely, so a merge-only resolution (no code diff against `R.Head` beyond main's files) does not hit the "changed no code" cap, then falls through to the existing label removal and `PhaseApplied` append. Not applied (head unmoved or not a merge including `R.Head`) caps with `why` "the last conflict round did not resolve the conflict" and reason conflict. Review rounds keep the existing `Covers` path untouched. Dry-run logs stay as they are.
4. `merger/loop.go`: add `type Rounds interface { RequestRound(ctx context.Context, r reviewfix.Request) (bool, error) }` (new) and `Rounds Rounds` on `Deps` (nil keeps today's behaviour). Check for an import cycle (merger already imports reviewfix? if not, `reviewfix` must not import merger; it does not today).
5. `merger/merge.go`: new `(t *tick) conflict(ctx, pr github.PullRequest, issue int, roles []string) bool` returning false (never merges). Replace the `needsHuman(ctx, pr, "conflict", "")` call at gate g (merge.go:121, pass `issue` and `v.Roles`) and in `updateBranch` (merge.go:317; thread `issue` and roles, it already has `issue`). Behaviour:
   - `t.d.Rounds == nil`, or `prgate.ParseWorkerBranch(pr.HeadRef)` fails, or the worker role is not in `t.cfg.Role(config.KindWorker, worker)`: fall back to `t.needsHuman(ctx, pr, "conflict", "")`, text unchanged.
   - read `reviewfix.Rounds(ctx, d.Store, issue)`; newest `requested` with `Head == pr.HeadSHA` (round already out): log `merge.conflict_round_pending` and return false; newest `capped` at this head: return false silently (a human owns it).
   - dry run: log `merge.would_request_conflict_round` and return false.
   - else `Rounds.RequestRound(ctx, reviewfix.Request{Issue: issue, PR: pr.Number, Head: pr.HeadSHA, Worker: worker, Reason: reviewfix.ReasonConflict, Roles: roles, Max: wr.MaxReviewRounds})`; on error `t.fail(pr.Number, "conflict_round", err)`. At the cap `RequestRound` runs `capPath` itself, so the merger adds nothing.
   - leave the mergeable-unknown / 405 retry paths as they are. Update the `needsHuman` "conflict" text only if still accurate when Rounds is nil (leave it as is: it points to part B).
6. `servecmd/merger.go`: no change except a comment on the call that `Rounds` is deliberately unset until part B (#255). Do not add a config key. Reason: avoids editing `.ard/config.yaml` and schema.
7. Docs: `merger/doc.go` gate g (note the request path and that it needs `Deps.Rounds`), `reviewfix/doc.go` (round reasons), and `docs/proposal.md` (line ~337 sentence "Merger conflict and rebase rounds are a follow-up" and line ~383 conflict bullet): describe the conflict round request path, that it counts toward `max_review_rounds`, that landing accepts a merge head including the round's head, and that it is off in production until part B (the merger's `Rounds` stays unset, so a conflict still goes to `ard:needs-human`). Run `make test` since `internal/docscheck` may check docs.

### Test plan

Test command: `make test` (`go test ./...`) and `make lint` (vet and gofmt).

- `merger/loop_test.go` `TestConflictRequestsRound`: ready PR with `Mergeable=false`, fake `Rounds`; one `Request` with `Reason: conflict`, all requested roles, `Max` from the worker role; no needs-human label or comment. File: `internal/merger/loop_test.go`.
- `TestConflictNilRoundsNeedsHuman`: `Rounds == nil`; same input gives the existing needs-human comment and label. All existing tests pass unchanged.
- `TestConflictRoundPendingIdempotent`: newest round requested at the same head gives no second request. `TestConflictCappedHumanOwned`: newest capped at the head gives no request and no comment.
- `TestConflictDryRun`: no request, no label.
- `TestConflictUnknownRetries`: `Mergeable == nil` and state `unknown` give no round and no label.
- `TestUpdateBranchConflictRequestsRound`: `UpdateBranch` 409 with a computed conflict requests a round; with a moved head it defers.
- `TestConflictCountsWithReviewRounds` (real `reviewfix.Loop` as `Rounds`): one review round plus a conflict round with `max_review_rounds: 2` reaches the cap; the capped event has reason conflict and the comment uses conflict wording.
- `reviewfix/loop_test.go`: `TestLandingConflictMergeHead` (head is a merge with `R.Head` as ancestor: applied event, requested reviewers' `:done` and `:changes` labels removed, no cap); `TestLandingConflictHeadUnmoved` (caps with reason conflict); `TestLandingConflictNonMergeDescendant` (a plain commit on top does not land); `TestLandingReviewUnchanged` (review round no-code cap still fires).
- `gitx`: `TestIsAncestor` (true, false, same commit) and `TestParents` on a scratch repo.
- Edge: round head equals pr head; criss-cross history; capPath marker unchanged for review.

### Risk

Low to medium. The production path is unchanged because `Rounds` stays nil in `servecmd`. The changed shared code is `capPath` (signature and event reason, same review output) and `landing` (a new branch for conflict rounds only), a state machine shared with the worker claim and event order. No file in `merge.protected_paths` is edited, no migration, no new config key. The public interfaces touched are `merger.Deps` (a new optional field) and `reviewfix.Git` (a new method).

### Size

M: about 250 changed lines including tests, and well under the worker block limit.
Files touched: `internal/reviewfix/{round.go,loop.go,doc.go,loop_test.go,harness_test.go}`, `internal/gitx/{inspect.go,inspect_test.go}`, `internal/merger/{loop.go,merge.go,doc.go,loop_test.go,harness_test.go}`, `internal/servecmd/merger.go` (comment only), `docs/proposal.md`.
