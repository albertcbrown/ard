// Package prwatch is the PR watch loop (#267). Every intervals.prwatch it
// reads ard's own pull requests (App-authored worker/ branches on main: draft,
// ready, merged and closed) and notices what happened to them behind ard's
// back, so the owner is told when a PR needs them and ard's state stays right
// when they act on a PR directly.
//
// The loop only observes, records and notifies. It never merges, updates a
// branch, re-requests a review or starts a round: those belong to the merger,
// the stale-review and conflict handling, and reviewfix. Its Effects interface
// has no such method, so the rule holds at the type level.
//
// What it does, each at most once:
//
//   - A PR closed without merging while its issue is open: the issue gets
//     ard:needs-human and one comment saying how to retry.
//   - Required checks pending on one head for longer than
//     pr_watch.pending_timeout: one status-issue notice per (PR, head).
//   - A PR merged by a human: a merge event with that actor.
//   - A draft PR whose required checks are red: one PR comment per head.
//
// Checks are judged by prgate.EvalChecks, the merger's rule. It acts only on
// PRs it has seen open (it keeps a row per PR in the controller database), so
// installing it does not replay history. Check names are CI-controlled text:
// they are sanitized and printed in code spans, and no PR or issue body text
// is ever copied.
//
// Idempotence is a triple check before each effect: the row flag, a pr_watch
// event for (pr, head, transition) and a marker comment on the target. A dry
// run performs the effects (which write only the ledger), keeps its state in
// memory and appends no events.
package prwatch
