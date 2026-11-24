// Package reviewfix sends a worker PR with blocking review findings back to
// its worker, with a cap.
//
// The loop runs no job and no agent. Labels carry the live state and the
// append-only review_round event carries the round count, in three phases:
//
//   - requested: every requested reviewer has finished, at least one ended
//     with :changes (proved by its close-out record) and the worker's :done
//     label was removed so the worker loop claims the issue again.
//   - applied: the worker's fix landed with code changes; every requested
//     reviewer's :done and :changes labels were removed so they rerun at the
//     new head.
//   - capped: max_review_rounds was reached, or a round changed no code. The
//     issue got one comment and ard:needs-human.
//
// Each round has a reason, review or conflict. A conflict round (requested by
// the merger through RequestRound) lands when the PR head is a merge commit
// that includes the round's head; a review round lands when code changed.
// Both count toward max_review_rounds.
//
// Events are compared by ID, never by time.
package reviewfix
