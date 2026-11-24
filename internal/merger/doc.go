// Package merger is the merger loop (#46): it squash-merges the PRs ard opened
// once every gate passes. It is controller code with no agent and no pool
// slot.
//
// The merger is the gate, not GitHub. The App bypasses main's whole ruleset
// (its direct .ard/ commits need that), so GitHub would accept a merge with red
// or pending checks. Every gate below is therefore checked in code, in this
// order, and every unreadable fact fails closed. A tick merges at most one PR
// and updates at most one branch, oldest PR first.
//
// # Candidates
//
// Open, non-draft PRs on base main that the App opened on a head branch
// worker/<role>-<issue#>-<slug>. A draft is the reviewer loop's to mark ready,
// but the merger re-checks every review itself and never trusts the flag. A
// candidate is skipped while the PR or its issue carries hold or needs-human,
// while the PR carries tests:failed or executor:failed, while the issue is
// closed and while a worker or review job of the issue runs.
//
// # Gates
//
//  1. Reviews: the label rule and head comparison of package prgate, shared
//     with the reviewer's mark-ready. Each requested review must be done at a
//     head that still covers the current one. An update-branch merge from main
//     does not invalidate a review (prgate.Covers with the merge base). A stale
//     review (code changed since it) is re-requested once per head: the merger
//     removes the role's :done label, records a review_rerequest event and logs
//     merge.review_rerequested (merge.would_rerequest in dry-run).
//  2. The body's first line is "Closes #<issue>" and a worker job of the issue
//     ran on this branch (its id becomes the Ard-Job trailer). Otherwise the PR
//     gets ard:needs-human.
//  3. Conflict: only a computed mergeable=false or mergeable_state dirty is a
//     conflict (ard:needs-human; with Deps.Rounds set, a conflict round is requested instead). A null or unknown mergeable, and
//     a 405 "not mergeable" from the merge call, are GitHub still computing and
//     mean retry next tick: never a label.
//  4. Up to date: the branch is behind when main changed outside
//     .ard/<digits>/ since the merge base. The merger calls UpdateBranch (at
//     most once per tick) and checks again next tick on the new head. Other
//     issues' .ard/ commits on main do not count, or the committer's close-out
//     pushes would keep every branch behind forever. GitHub's merge API guards
//     the head SHA, not the base, so a push to main between the merger's fetch
//     and the merge goes unseen.
//  5. Checks, from check runs only (commit statuses are not read). The
//     required list comes from the branch rules API: when it is readable and
//     names checks, each must be completed and green on the head (neutral and
//     skipped count as green, as on GitHub); red goes to ard:needs-human with
//     one comment naming them, missing or running ones wait. When the rules are
//     unavailable (a free private repository) or none require checks, there is
//     no list to go by: with no check runs on the head the merger merges on
//     reviews alone, but only after a grace period since it first saw the head
//     (at least one tick and Deps.NoCIGrace, default 2 minutes) so CI that has
//     not queued yet is not missed; with check runs, every latest run per name
//     must be green. A rules read that fails (5xx, network, permissions) merges
//     nothing. Rule types the client cannot evaluate (workflows, code
//     scanning, required deployments) are logged at warn.
//  6. Protected paths: when a changed path (both names of a rename; a
//     malformed path counts as protected) matches merge.protected_paths, a
//     GitHub review in state APPROVED, from a user who is not an App and has
//     write access or more, must stand at a commit that still covers the head.
//     A later CHANGES_REQUESTED or DISMISSED review by the same user voids it.
//     Without it the PR gets the informational ard:needs-approval label and one
//     comment per head.
//
// The merge is a squash with the PR title plus " (#<pr>)" and one Ard-Job
// trailer per worker job, guarded by the head SHA. The branch is deleted
// afterwards and the PR body's "Closes #<n>" closes the issue.
//
// # Pause and dry-run
//
// Claims is true, so the loop is skipped while the controller is paused; a
// tick in progress may finish. In dry-run the same gates run and every write
// goes to the effects ledger; the loop logs merge.would_merge and
// merge.would_update and keeps going after a would-merge.
package merger
