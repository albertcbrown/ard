// Package closeout turns a finished agent job into labels, a comment and
// commits. It is a library: the loops (Phase 1) call it; there is no CLI.
//
// Prepare is the pure half. It reads the job's worktree and host log, parses
// and validates .ard/<issue#>/<stage>-<role>.md (trusted only if the job wrote
// it: the executor scrubs a stale one before the agent runs), collects the
// changed .ard/<issue#>/ files, checks them against the job's write allowlist
// (WritableFor: its role file, plus plan.md section and design/ files for the
// triage roles that own them; any other change fails the job as needs-human and
// only the log is committed), redacts and secret-scans every one, stages the cleaned bytes under
// Deps.StagingDir and returns a Record holding the Plan. Closer is the
// effectful half. It runs the plan in a fixed order (commit, request labels,
// comment, terminal label swap) and persists the phase after each step, so
// Resume can finish an interrupted close-out after a crash without a second
// comment, a lost label or a re-read of a changed file.
//
// Every GitHub write goes through effects.Effects and every commit through the
// committer (CLAUDE.md invariants 1 and 5). Agent text reaches GitHub only
// after redaction and sanitising.
//
// Caller contract, for the loops (#29, #33, #35) and the Reaper (#37):
//
//   - serve, the loops and the Reaper share a single *Closer from New: the
//     per-job busy lock only serialises Run, Resume and ResumeJob within one
//     instance. A caller that gets ErrBusy skips the job and looks again.
//   - serve startup calls the Reaper's ResumeAll (which uses ResumeJob) after
//     taking the host lock and before adopting running jobs.
//   - Every way a job can end goes through Run, including a Reaper failure and
//     a transient retry: set Job.Cause (reaper.Finisher does this) and Run
//     commits only the log and applies the failed or retry plan. Callers do
//     not post failure comments or swap labels themselves. Use
//     reaper.Finisher.Finish, which also ends the attempt and tidies up.
//   - The Reaper must not mark a job failed while its close-out phase is set
//     and not closed: the agent has finished and a close-out is in flight. It
//     resumes that job instead.
//   - Outcomes: done, changes and needs-human are results; failed sets
//     ard:executor:failed; retry (a named transient cause) does not, and keeps
//     the request label so the next claim runs the job again as a new job.
//     Failed and retry comments carry the last 50 redacted log lines and the
//     time the worktree and host files are kept until (24 h).
//   - Closer never releases the pool slot or removes the worktree. The caller
//     does both, keeping the worktree when the outcome is failed, retry or
//     needs-human and removing it otherwise.
package closeout
