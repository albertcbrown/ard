// Package triage is the triage loop (#29): it turns an issue into plan.md, one
// triage role at a time, writing only to .ard/<issue#>/ on main.
//
// A tick never waits for a job. It sweeps intake, finds candidates, asks the
// pool for slots and hands each granted job to a watcher goroutine
// (controller.Inflight). The watcher follows the contract in package reaper:
// create the job row, add the :running label, run the agent, call
// Finisher.Finish, and only then release the slot. Close-out, the committer,
// the executor and the Finisher are the process-wide shared instances; this
// package builds none of them.
//
// Untrusted text (the issue and its comments) reaches the agent as files under
// .ard-input/<issue#>/ in the worktree. That directory is outside the
// .ard/<issue#>/ pathspec close-out collects, so it is never committed.
//
// In --dry-run the loop logs triage.would_claim and stops: no job row, no pool
// slot, no label and no comment.
package triage
