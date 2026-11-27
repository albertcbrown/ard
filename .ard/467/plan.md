# Plan for #467

## Implementation

### Steps
1. `internal/worker/conflict.go`: add to `conflictRound` a new field `clean bool` (applies, trial merge clean). In `conflictInput`, when `MergeNoCommit` returns no paths, set `cr.clean = true` and keep the scratch worktree for the caller: new fields `wt string` and `release func()` (removes the worktree via `WorktreeRemove` with `context.WithoutCancel` and `os.RemoveAll(scratch)`); the existing `defer`s must only run on the non-clean paths and error paths (use a `keep` flag). Reason: the merged-but-uncommitted tree is what the guard and committer need.
2. `internal/worker/run.go`, `run`: when `cround.clean`, skip adding review feedback (already so via `applies`), create the claim record and running label as today, build the `job`, set `jb.mainTip = cround.mainTip`, and call new `jb.cleanMergeRound(ctx, cround)` instead of `jb.exec`. Always call `cround.release()` (defer). Never reach `agentStep` when `clean`.
3. `internal/worker/run.go` (new) `func (jb *job) cleanMergeRound(ctx, cr conflictRound)`: `StartAttempt`; log `worker.conflict_clean_merge` (branch, main tip); run `l.facts(ctx, n, cr.wt, cr.mainTip, "", stage)` on the scratch worktree against the main tip, then `Check(...)` with the role's `Lines`, exactly as in `round` (reuse by factoring the guard-and-push part of `round` into a helper taking worktree and base, or by passing a synthetic `step{h: executor.Handle{Worktree: cr.wt, BaseSHA: cr.mainTip}}`); then `jb.push(ctx, s, v)` which sets `MergeParent = mainTip` and commits with parents `[branch tip, main tip]` through `CommitCode`. Because the merge result may equal main's tree plus branch changes, a merge with zero declared files must still be committed (the code lane already commits a merge whose tree equals main's). On any hold or push failure use the existing paths (`finishWithPR` with the same causes as `push`: ErrConflict and branch-moved give `CauseNoProcess`, transient gives `CausePushTransient`); never start an agent. On success call `jb.finish(ctx, step{}, &closeout.WorkerResult{Tests: closeout.TestsNone, Pushed: true}, closeout.Cause{})` (or `finishWithPR`, which opens/updates the PR if needed) so the round bookkeeping (`reviewfix` landed check: head is a merge including the round's head with second parent on main) runs. Check that `FinishWith` copes with an empty `step` (no handle, no role file); if close-out requires a role file, synthesise the done result in the worker instead of reading one, and keep it controller-written (no agent text).
4. No tokens: no `execJob`, no `agentStep`, no `jb.cost` entries. Confirm the finisher records zero cost for a job with no usage.
5. `docs/proposal.md`: in the conflict-round paragraph (the one that begins "The merger can request a conflict round", under "Review feedback loops back here"), add one sentence: a clean trial merge is merged and pushed by the controller itself, with no agent job, and the round closes as done.

### Network inputs
None.

### Test plan
Command: `go test ./internal/worker/...` (then `go vet ./...`).
- `TestConflictRoundCleanMerge` (`internal/worker/conflict_round_test.go`): update. Setup `conflictSetup(..., false)` + `requestConflictRound`. Expect `cr.applies`, `cr.clean`, `cr.input == nil`, `cr.mainTip` equal the remote main tip, no conflicted paths; call `cr.release()`.
- New `TestConflictRoundCleanMergePushes` (same file): run the loop through the harness (`h.agentWrites` as in `TestConflictRoundResolved`) with a run hook that fails the test if any executor job of kind agent starts. Expect no executor agent job, the remote branch head is a merge commit whose second parent (`git rev-parse branch^2`) is the main tip and first parent is the old branch tip, the tree contains both sides' changes, close-out outcome done, round closed, `worker.conflict_clean_merge` logged.
- New `TestConflictRoundCleanMergeBranchMoved`: advance the remote branch after the trial merge (hook between fetch and push). Expect `ErrConflict` handled as `push_failed`, job ends like other push failures, still no agent job and nothing pushed.
- New `TestConflictRoundCleanMergePushError` (transient): fake push error gives `CausePushTransient`, no agent.
- Edge: clean merge touching a protected path (`.github/`) goes through the existing guard and committer rules; main already an ancestor of the branch (nothing to merge): push still produces a merge commit or the round ends as the committer decides (document in a test).
- Existing conflict tests (`TestConflictRoundInput`, `TestConflictRoundResolved`, `TestConflictRoundNotARound`) must still pass.

### Risk
Medium. Touches the worker's push path and close-out for a round type; security-sensitive because the commit is created from a worktree the controller built (keep it going through the diff guard and the code lane, never `git push` directly). No protected paths (`.github/`, `.ard/config.yaml`, `.ard/scanners/` and so on) are edited. No public interface or migration changes. Concurrency: branch can move between fetch and push, which the committer's lease and `ErrConflict` already cover.

### Size
M to L (about 150 to 300 changed lines including tests). Files: `internal/worker/conflict.go`, `internal/worker/run.go`, `internal/worker/conflict_round_test.go`, `docs/proposal.md`.
