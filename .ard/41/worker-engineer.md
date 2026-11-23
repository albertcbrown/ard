---
role: engineer
status: done
attempt: 1
---
Adds the `reviewfix` loop: a worker PR with proven `:changes` findings goes back to its worker, every requested reviewer reruns once the fix lands with code changes, and the issue goes to `ard:needs-human` at `max_review_rounds` or when a round changes no code. Round counts live in a new `review_round` event in `_controller.db`.

## Changes
- `internal/state`, `internal/closeout`: `EventReviewRound` and `closeout.ReviewChanged` (a `:changes` label needs a close-out record).
- `internal/reviewfix/` (new): `Rounds`, `Requested`, `RequestRound` (event first, then the worker `:done` label removal) and the loop with its quiet wait, lost-removal replay, apply, and cap path.
- `internal/servecmd`: wires the loop after the reviewer loop and adds it to `knownLoops`.
- `internal/worker`: a running review job makes the issue busy; `review-feedback.md` (round, committed role files, human PR comments, 64 KiB cap) and `review-` files as untrusted inputs.
- `internal/reviewer`: `previous-review.md`, read at the pinned head.
- `internal/prgate`: the worker round-trip test moved to an external test package to avoid an import cycle.
- `docs/proposal.md`: worker loop, worker inputs, reviewer input and merger follow-up text.

## Testing
`make lint test` passes. New tests: `TestHandle` table (22 rows) and idempotence and failure-isolation tests in reviewfix; `TestReviewFixRoundEndToEnd` (changes, fix, approve, PR ready, one round) and `TestReviewFixCapEndToEnd`; `TestReviewFeedbackInput`; a busy-review row in the candidates test; `TestPreviousReviewInput`; the loop name lists.

## Open issues
- For a maintainer, add this Layout line to CLAUDE.md: `internal/reviewfix/  review fix rounds: the review_round event, Rounds/RequestRound, and the loop that sends :changes PRs back to the worker and reruns reviewers`.
- The same-timestamp claim case is covered by ID ordering throughout; no separate frozen-clock row was added.
- The dry-run and skip rows assert no events and no writes, but the dry-run row does not check the log lines.
