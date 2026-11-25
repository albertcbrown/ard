# Plan for #411

## Implementation

### Steps
Finding: this is a production race, not a test quirk. `internal/gitx/local.go` `WorktreeAdd` and `WorktreeRemove` take `wtlock` (via `worktreeLock`), but `fetch` (used by `FetchRef` and `FetchShallow`, so by `lane.fetch` in `internal/committer/committer.go` and by `internal/committer/init.go`) takes no lock. `docs/proposal.md` says outright that "fetches ... do not take it". A fetch can therefore read `.git/worktrees/*` while a lane is adding or removing one.

Fix: make the lock shared/exclusive. Fetches take it shared, so they still run in parallel and a slow network fetch never blocks other fetches. Worktree add/remove/prune take it exclusive, as today.

1. `internal/wtlock/wtlock.go`: add `RLock(ctx, commonDir) (unlock func(), err error)` (new). Keep `Lock` as the exclusive form with the same signature and semantics (ctx-aware, symlink-resolved key, idempotent unlock). Implement it per key with a writer gate (`chan struct{}`, cap 1) plus a read-slot semaphore (`chan struct{}`, cap 64, new const).
   - Reader: take the gate, take one slot, release the gate.
   - Writer: take the gate, take all slots one by one, and release the slots and the gate on unlock.
   - Holding the gate stops new readers, so writers cannot starve.
   - Every wait selects on `ctx.Done()`, and a partially acquired writer releases what it holds when the ctx ends.
   - Update the package comment: fetches take the shared side.
2. `internal/gitx/local.go`: add `fetchLock(ctx, repoDir) (func(), error)` (new), sharing the common-dir lookup with `worktreeLock` (extract a helper `commonDir`).
   - `fetch` calls it before `g.run`, after the token is minted so the token wait is not under the lock, and defers unlock.
   - If `rev-parse --git-common-dir` fails because `repoDir` is not a repository yet (a first fetch into a fresh dir), fetch proceeds unlocked. There are no worktrees to race then.
   - Update the `WorktreeAdd` and `WorktreeRemove` comments.
   - Do not hold the lock when the fetch ctx has no deadline beyond the caller's: the exclusive side has a 30 s timeout, so a long fetch (up to 30 min on the seed) can delay add/remove past that timeout. Mitigation: `worktreeAdminTimeout` should apply only to the git command, with the lock wait on the parent ctx. Make that change in `WorktreeAdd` and `WorktreeRemove` (take the lock with the caller's ctx, `WithoutCancel` plus a generous bound for remove, then start the 30 s timer). Alternatively leave it and accept add failing with a lock-wait timeout, which the job reports. Prefer the first.
3. `docs/proposal.md`, in the local-executor paragraph (the sentence "fetches, commits and pushes do not take it"): state the concurrency rule. Add/remove/prune take the clone lock exclusively and fetches take it shared. Commits and pushes take neither. Add the same one-line rule to the committer paragraph ("One committer for main"). Run `make docs-check` if it exists.
4. Tests (below).

### Test plan
- `internal/wtlock/wtlock_test.go` (new cases):
  - `TestRLockShared`: two `RLock` on one dir both succeed.
  - `TestRLockBlocksLock`: with an `RLock` held, `Lock(short ctx)` returns DeadlineExceeded. After unlock, `Lock` succeeds.
  - `TestLockBlocksRLock`: with a `Lock` held, `RLock(short ctx)` returns DeadlineExceeded.
  - `TestWriterNotStarved`: a writer waiting behind a reader blocks a new `RLock`.
  - `TestRLockCancelledCtx`, `TestRLockMissingDir`: same errors as `Lock`.
  - `TestWriterCancelReleases`: a writer cancelled mid-acquire leaves the lock usable.
  - `TestRUnlockIdempotent`.
- `internal/gitx/gitx_test.go` or `checkout_test.go` (new `TestFetchSerialisedWithWorktreeAdd`): hold `wtlock.Lock` on the clone's common dir, start `FetchRef` in a goroutine, assert it has not finished after about 100 ms, release, and assert it finishes. This fails before the fix and passes after. It forces the interleaving without timing luck.
- `internal/gitx` (new `TestFetchSharedWithFetch`): two concurrent fetches do not block each other. Use `RLock` held by the test.
- `internal/committer/committer_test.go`: keep `TestBranchesStress`. It was `t.Parallel`-free and flaky, and it is the regression guard.
- Commands: `go test ./internal/wtlock ./internal/gitx ./internal/committer`, then `make lint test`. Local acceptance check: `go test -count=200 -race ./internal/committer -run TestBranchesStress`, also under CPU load.
- CI: there is no repeated `-count=200` race run in CI (`make test` is `go test ./...`). That run is a local check for the worker and the reviewer only. Do not add it to CI.
- Edge cases: ctx cancelled while waiting for the shared lock returns a wrapped ctx error. A fetch into a not-yet-existing repo still works (unlocked).
- Do not add a retry on `failed to read .git/worktrees/*/commondir`. The lock is the fix. A retry is optional and out of scope.

### Risk
Medium. The change is concurrency code on the committer's close-out path.
- Deadlock risk: no code path holds the exclusive lock and calls fetch, nor the reverse. `WorktreeAdd` runs only git under the lock. Check that no committer caller holds a worktree lock while fetching.
- The lock is per process and cannot cover a human's git in the clone. That is already documented.
- Not a protected path (`.github/`, `.ard/config.yaml`, `.ard/scanners/`, `.ard/triage/` and so on). Public interfaces: `wtlock` gains `RLock`, and `gitx` signatures do not change.
- No migrations. Not security-sensitive.

### Size
M. Files: `internal/wtlock/wtlock.go`, `internal/wtlock/wtlock_test.go`, `internal/gitx/local.go`, `internal/gitx/gitx_test.go` (or `checkout_test.go`), `docs/proposal.md`. About 200 changed lines, well under diff_max_lines (3,000 default).
