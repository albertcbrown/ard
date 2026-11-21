// Package closeout turns a finished agent job into labels, a comment and
// commits. It is a library: the loops (Phase 1) call it; there is no CLI.
//
// Prepare is the pure half. It reads the job's worktree and host log, parses
// and validates .ard/<issue#>/<role>.md, collects the changed .ard/<issue#>/
// files, redacts and secret-scans every one, stages the cleaned bytes under
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
// Caller contract, for the loops (#15), the pool (#16) and the Reaper (#37):
//
//   - serve startup calls Closer.Resume after taking the host lock and before
//     adopting running jobs.
//   - The Reaper must not mark a job failed while its close-out phase is set
//     and not closed: the agent has finished and a close-out is in flight. It
//     resumes that job instead.
//   - A job with no phase and an exited process is passed to Closer.Run.
//   - Closer never releases the pool slot or removes the worktree. The caller
//     does both, keeping the worktree when the outcome is failed or needs-human
//     and removing it otherwise.
package closeout
