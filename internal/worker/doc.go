// Package worker is the worker loop (#33): it turns a planned issue into a
// draft pull request, one worker role at a time.
//
// A tick never waits for a job. It finds the issues that request a worker role
// (ard:worker:<role>, added by the triage fan-in gate), re-checks approval and
// admission, asks the pool for slots and hands each granted job to a watcher
// goroutine (controller.Inflight). The watcher follows the contract in package
// reaper: create the job row, add the :running label, run, call
// Finisher.FinishWith and only then release the slot.
//
// # One job, several executor runs
//
// A worker job is one state row, one pool slot, one attempt and one Ard-Job
// id, but it runs a sequence of executor jobs ("steps") in fresh worktrees:
//
//	agent   <jobID>     branch head, else the config SHA   writes code and worker-<role>.md
//	test k  <jobID>t<k> the SHA the last push made         /bin/sh -c <tests>, no credential
//	fix k   <jobID>f<k> the same SHA                       like agent, plus test-output.log
//
// Every step's handle is saved before it starts, so the Reaper's heartbeat and
// deadline rules apply to the step that runs. role.timeout covers the whole
// job: each step gets the remaining time, and with under two minutes left the
// loop stops and reports the tests as failed.
//
// # Two commits per round
//
// After each agent or fix step the diff guard (guard.go) decides whether the
// change may be committed. If so the loop commits the code, everything outside
// .ard/ and .ard-input/, to worker/<role>-<issue#>-<slug> through
// committer.Branches.CommitCode, with the Ard-Job trailer. At the end close-out
// commits worker-<role>.md and the redacted logs to the same branch (or to
// main when the job stopped before its first push), so .ard/<issue#>/ always
// goes through close-out's redaction and per-role allowlist and never through
// the code lane.
//
// # Inputs
//
// The issue and its comments reach the agent as untrusted files under
// .ard-input/<issue#>/ (issue.md, comments.md and, on a fix round,
// test-output.log), outside the .ard/<issue#>/ path close-out collects, so
// they are never committed. plan.md and the triage role files are untrusted
// too.
//
// In --dry-run the loop logs worker.would_claim and stops: no job row, no pool
// slot, no label, no comment and no PR.
package worker
