// Package reaper detects jobs that failed and keeps their evidence, and owns
// the one way every job ends.
//
// # The rule
//
// Every way a job can end goes through closeout.Closer.Run: a normal result, a
// crash, a deadline, a quiet heartbeat, a transient retry. The Reaper never
// posts a failure comment or swaps labels itself, so the stored close-out
// record and the per-job busy lock are the one guard against a double comment,
// and a crash in the middle of reaping is resumed like any other close-out.
// The one exception is a :running label that has no state row: with no row
// there is no record to resume (norecord.go).
//
// # Ownership
//
// The Reaper decides each running job from the pool slot and the executor
// status, never from a timer guess. A slot that was not adopted and whose job
// id is empty or the job's own means a goroutine in this process is driving
// the job (it will close it out): the Reaper may only kill it, for a deadline
// or a quiet heartbeat, and first records why (state.SetKillReason) so the
// owner's Finisher reads it. Every other job (adopted after a restart, no
// slot, or a slot that belongs to another job) is closed out by the Reaper
// itself, and its adopted slot is released.
//
// # Contract for the triage, worker and reviewer loops (#29, #33, #35)
//
//  1. pool.Offer, and only on Granted:
//  2. state.CreateJob (NewJob.Branch for worker and review jobs), then
//     Slot.SetJobID, then add the :running label.
//  3. Run the job. Call state.StartAttempt exactly once.
//  4. Call Finisher.Finish with the executor handle and status. It closes the
//     job out through the shared *closeout.Closer, ends the attempt and tidies
//     the worktree. It re-reads the job row, so a kill reason the Reaper
//     recorded is honoured.
//     A worker job calls state.SetBranch (after its first push) and SetPR
//     first, then Finisher.FinishWith with the handle of its final agent step,
//     the closeout.WorkerResult and the logs of its earlier agent steps.
//     A worker job runs several executor jobs (agent, tests, fixes) under one
//     state job; the loop saves each step's handle before starting it, so the
//     Reaper's heartbeat and deadline rules apply to the step that runs.
//     A review job calls Finisher.FinishWith with FinishOptions.Review (the
//     added lines for inline placement and whether the PR head moved); a
//     review closed out without it lists every finding in the review body.
//  5. Then Slot.Release, deferred so a panic still frees it.
//
// Never release the slot before close-out has finished.
//
// # Retries
//
// Every retry, automatic or human, is a new job with its own id. A named
// transient cause (rate_limit, clone_failed, push_transient, controller_restart) hands off to
// one automatic retry: the failed job gets no ard:executor:failed, keeps its
// request label, and the next claim is linked to it through retry_of. An
// automatic retry that fails again is marked failed.
//
// A usage-limit stop is not a failure. UsageLimitClassifier names it as its
// own cause (usage_limit) and the close-out outcome is usage-limit: the job
// gets no ard:executor:failed and keeps its request label, so the issue is
// claimed again once the pool's pause for the limit ends (Finisher.Limits
// records the pause before anything is posted). A limit stop neither uses up
// nor grants the automatic retry; the re-run is a new job linked to the stopped
// one through after_limit, which carries retry_of over.
package reaper
