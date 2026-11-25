---
role: engineer
status: done
attempt: 1
---
When a worker pushes its branch but GitHub will not open the pull request, the loop now retries the create with backoff, and on final failure records the close-out so a later label removal opens the PR without running the agent.

## Changes
- `internal/worker/prretry.go` (new): `createWithRetry` (4 attempts, 2s/5s/15s backoff, transient errors only via `github.IsTransient`, looks for an existing PR on the head before each retry, no retry under dry-run), `failCreate` and `reopenPR`. The reopen path runs before the agent step. It needs a record from the newest earlier worker job for the issue, a branch tip equal to the recorded head (or only adding files under `.ard/`, the failed job's own close-out commit) and no open PR. Otherwise the full run happens.
- `internal/worker/run.go`, `loop.go`: `openPR` uses the retry. After a push, a failed create ends with the new cause `pr_create_failed` and stores the record. Without a push it keeps `no_process`. New `Deps.Sleep` and two `Store` methods.
- `internal/closeout`: new `CausePRCreateFailed` (failed plan, not transient, no retry). `WorkerResult.Reopened` and `Summary` let `Prepare` close a job done with no role file or agent worktree.
- `internal/state`: migration 0009 adds a nullable `pending_pr` column, so old rows are unaffected. Also `PendingPR`, `SetPendingPR` and `LatestPendingPR`. The schema golden and the schema-version assertion are updated.
- `internal/roles/workers/engineer/prompt.txt`: rule to answer `done` when the branch already holds the complete change and no PR exists. `docs/proposal.md`: worker step 8 describes the recovery.

## Testing
`make lint test` passes. New tests use the GitHub fake: retry then open (no second agent step, no `executor:failed`), non-transient error not retried, always-fails records `pr_create_failed` with the branch and head, reopen without an agent, reopen tolerating the close-out `.ard/` commit, branch moved falls back to the full run, existing PR falls back with no duplicate, dry-run does not sleep or retry, sleep stops on cancel, no-push failure keeps `no_process`. Closeout tests cover the reopened result and the `pr_create_failed` plan; a state test covers the record and its supersession. An earlier run of the state tests failed on the schema version and golden file; both were updated.

## Open issues
- `.ard/workers/engineer/prompt.txt` (protected) is not changed; a maintainer must sync the new prompt rule there by hand.
- Dry-run never reopens a PR from a record; it only logs.
- A cancelled context during backoff leaves the job running for the Reaper rather than finishing it.
