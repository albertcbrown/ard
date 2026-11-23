// Package reviewer is the reviewer loop (#35): it reviews the pull requests
// ard opened and returns a verdict that close-out turns into one PR review and
// a label.
//
// A tick never waits for a job. For each reviewer role it finds open PRs that
// request it (ard:review:<role>, added by the worker's close-out), re-reads
// each PR and its issue, asks the pool for slots and hands each granted job to
// a watcher goroutine (controller.Inflight). The watcher follows the contract
// in package reaper: create the job row, add the :running label to the PR, run,
// call Finisher.FinishWith and only then release the slot.
//
// # Which PRs
//
// Only PRs that the App opened, with base main and a head branch of the form
// worker/<role>-<issue#>-<slug> (see parseWorkerBranch). Human PRs are skipped
// and logged. A PR is also skipped while its issue is closed, held or needs a
// human, while a worker job for the issue runs, when its issue was edited
// after admission (the issue is re-gated, not reviewed), and when it carries
// ard:tests:failed. Reviewers of different roles run in parallel on one PR.
//
// # The pin
//
// Before it claims anything the watcher fetches the branch and requires the
// tip to equal the PR's head SHA; a mismatch is retried next poll. That also
// shows the head belongs to this repository's branch, so a fork PR whose
// branch name collides is never reviewed. The job's worktree is cut at that
// commit, and the review is posted against it.
//
// # Inputs
//
// All of it is untrusted data under .ard-input/<issue#>/, outside the path
// close-out collects, so none of it is committed: diff.patch (the diff from
// the merge base with main to the pinned head, with --text so .gitattributes
// cannot hide a change, .ard/ left out, at most 1 MiB), issue.md and
// comments.md. The committed .ard/<issue#>/ (plan, triage and worker role
// files and logs) and the sidecar are untrusted too.
//
// # The verdict
//
// The agent writes only .ard/<issue#>/review-<role>.md; close-out rejects any
// other change (WritableFor). Code edits are never collected. Close-out posts
// one COMMENT review at the pinned commit (inline comments on lines the PR
// added, everything else in the body) and swaps the label: :done, :changes, or
// (an approve on a head that gained code meanwhile) neither, so the request
// stays and the review runs again. The loop never edits the PR.
//
// # Marking ready
//
// After each close-out, and on every tick (so a restart cannot miss it), the
// loop marks a draft App PR on a worker/ branch ready for review when every
// requested review role is :done at the PR's current head (the head recorded
// by its newest claim, ignoring commits that only touch .ard/<issue>/), and
// none of :changes, :running, ard:executor:failed, ard:needs-human, ard:hold or
// ard:tests:failed is set. It logs review.marked_ready; a failed MarkReady is
// logged and retried next tick, never failing close-out. In --dry-run only the
// effects ledger gets a line. It never converts a PR back to draft and never
// merges.
//
// In --dry-run the loop logs review.would_claim and stops claiming: no fetch, no job
// row, no pool slot, no label.
package reviewer
