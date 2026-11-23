# Plan for #268

## Implementation

The premise "serialize add/remove/prune per repository" is already true: `internal/wtlock` (process-wide, keyed by the git common dir) is taken by `gitx.WorktreeAdd` and `gitx.WorktreeRemove` (`internal/gitx/local.go`) and by `execGit.admin` (`internal/executor/local/git.go`). `TestWorktreeAdminConcurrent` in `internal/gitx/gitx_test.go:292` already covers add/remove. The race therefore comes from something that touches `.git/worktrees` outside the lock. A local run of `go test -race -count=30 -run TestBranchesStress ./internal/committer` passed (git 2.56.1), so the cause is unconfirmed. The leading suspect is git's own background `gc --auto` / auto-maintenance, started by fetch, commit or push in a lane. It runs `worktree prune` detached and outlives the lock.

### Steps
1. Diagnose first. Run `go test -race -count=50 -run TestBranchesStress ./internal/committer`, with `GIT_TRACE=1` in a scratch run if it fails, and look for a `gc`, `maintenance` or `worktree prune` process not started by gitx. Record the finding in the PR description. If something other than auto-gc is found, adapt steps 2-3 to that source and keep the rest.
2. `internal/gitx/gitx.go`, `(*Git).run` (line ~58): add `-c gc.auto=0 -c maintenance.auto=false` to the base args next to `core.hooksPath` and `core.fsmonitor`. No git command run by the controller then starts a detached prune. `internal/servecmd/clone.go` (`gcOff`) already does this for the serve clone, so this extends the same rule to the committer's git calls. Update the comment there to say why.
3. `internal/executor/local/git.go`, `execGit.run`: apply the same two `-c` options if its run helper does not go through gitx. Check first, and skip if it does.
4. `internal/gitx/local.go`, `WorktreeAdd`: as a backstop, retry once, still under the lock, when the error contains "could not open" and "/locked" (admin dir vanished). Run `worktree prune` before the retry. Skip this step if step 1 shows the cause is fully removed by step 2, to keep the change small.
5. Tests, as below.

### Test plan
- `TestRunDisablesAutoGC` in `internal/gitx/gitx_test.go` (new). Input: run `g.run` with `config --get gc.auto` and `config --get maintenance.auto` in a temp repo. Expected: `0` and `false`.
- `TestWorktreeAdminWithAutoGC` in `internal/gitx/gitx_test.go` (new). Set `gc.auto=1` and `gc.autoDetach=true` in the repo config, create many loose objects, then run concurrent `FetchRef` calls alongside add/remove cycles on distinct lane names, as `TestWorktreeAdminConcurrent` does. Expected: no add error.
- Existing: `TestBranchesStress` in `internal/committer/committer_test.go:~495` is unchanged.
- Command: `go test -race ./internal/gitx ./internal/committer ./internal/executor/...`, then `go test -race -count=50 -run TestBranchesStress ./internal/committer`. A full 50-count run takes about 70 s on a laptop (30 runs took 40 s), so the worker should run it in full, and use `-count=20` only if the time budget is tight, saying so in the PR.
- Failure case: if the retry in step 4 is added, add a test where a persistent add failure is returned after one retry and not looped.

### Risk
Low to medium. No protected paths from `merge.protected_paths` are touched. `run` is used by every gitx call, so the `-c` options apply everywhere. Auto-gc is already off in the serve clone, so behavior there is unchanged. Test repos can now grow loose objects, which is harmless. The main risk is that the real cause is something else, hence the diagnosis step. Concurrency-related, but there are no migrations or public interface changes.

### Size
S to M (about 40-120 changed lines). Files: `internal/gitx/gitx.go`, `internal/gitx/local.go` (only if step 4 is kept), `internal/gitx/gitx_test.go`, and possibly `internal/executor/local/git.go`.
