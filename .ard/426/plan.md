# Plan for #426

## Implementation

### Steps
1. `internal/gitx/gitx.go`: add `ErrProtected = errors.New("gitx: push rejected: protected branch")` (new) next to `ErrStaleLease`. Not retryable by itself.
2. `internal/gitx/write.go` `Push` (~line 85): add a case after the stale-lease and non-fast-forward cases and before `classifyTransient`: `strings.Contains(stderr, "GH006")` or "Protected branch update failed" returns `fmt.Errorf("gitx: push %s: %w", op.Ref, ErrProtected)`. Order matters: a message that also says stale info or non-fast-forward keeps its existing class. Update the doc comment on `Push`. Prefer extracting the stderr-to-error mapping into a small pure function (new, e.g. `classifyPush(stderr string) error`) so a table test can call it without git.
3. `internal/effects/effects.go` (~line 33): add the alias `ErrProtected = gitx.ErrProtected`.
4. `internal/committer/errors.go`: add `ErrProtected` (new, "committer: push rejected by branch protection") for callers. Wrap the effects error so `errors.Is` matches both.
5. `internal/committer/committer.go` `lane.attempt` (~line 513): keep returning `retry=false` for ErrProtected. In `lane.run` (~line 384), before `rejections++`, handle `errors.Is(err, effects.ErrProtected)`:
   - if `!l.persistent` (branch lane): return the error wrapped with `ErrProtected`, no retry, no fetch.
   - if main lane: call `l.fetch(ctx)` to get the new tip. A transient fetch error follows the existing transient path (budget `maxTransient`). If the tip differs from `base` (the lease SHA the push used), it is a lost race: log `committer.retry` at Info with `reason=protected_race`, `old=<base>`, `new=<tip>`, then go through the existing rejection accounting (`rejections++`, the `maxPushes` check, backoff, `prev = base`) and loop. If the tip equals `base`, log `committer.failed` with `reason=protected` and return an error wrapping `ErrProtected` whose text names the likely fix (check the `main` ruleset's bypass list for ard's App; run `ard setup --check`).
   - Implement as a small helper (new) so `run` stays readable; the deferred `committer.failed` in `commit` already logs, so add `reason=protected` there when `errors.Is(err, ErrProtected)`.
   - Rejections (stale lease, non-fast-forward, protected race) share `rejections`; transients keep `transients`. Do not merge them.
6. `internal/closeout/closeout.go`: add `CauseProtected CauseKind = "push_protected"` (new, non-transient, never retried). In the commit-failure handling near line 640, add a case before the generic `failedPlan`: `errors.Is(err, committer.ErrProtected)` sets `rec.Job.Cause = Cause{Kind: CauseProtected, Detail: ...}` and builds `failedPlan` with a reason that names the main ruleset bypass list and `ard setup --check`. Keep the tail/keep fields as the neighbouring cases do.
7. `internal/worker/run.go` (~line 634): add `case errors.Is(err, committer.ErrProtected): kind = "protected"` so the failure is named; the branch lane still fails at once (cause `CauseNoProcess` is the existing fallthrough, or `CauseProtected` if simpler).
8. `docs/proposal.md`, "One committer for main" paragraph (line ~183): state that a GH006 rejection is retried only when `main` moved since the lease (counted against the rejection budget of 4 pushes), otherwise it is a protection failure with cause `push_protected`, and that branch-lane GH006 is never retried.

### Test plan
Command: `make lint test` (the worker role's `tests` setting); targeted: `go test ./internal/gitx ./internal/committer ./internal/closeout ./internal/worker`.
- `internal/gitx/gitx_test.go` (or a new test file), table test for the stderr classifier: GH006 text (porcelain stdout form and stderr form) gives ErrProtected; "stale info" gives ErrStaleLease; "non-fast-forward" gives ErrNonFastForward; GH006 text together with a 500 gives ErrProtected; "Internal Server Error" gives ErrTransient; unrelated text gives none of them.
- `internal/committer/protected_test.go` (new), reusing `env`, `logBuf` and a wrapper like `flaky` in `transient_test.go`:
  - `TestProtectedRaceRetried`: wrapper's first push moves main (commit on the remote through `e.live()`) and returns ErrProtected; expect success with `Attempts == 2`, main equals `res.SHA`, log has `reason=protected_race` and both SHAs.
  - `TestProtectedNotMovedFailsAtOnce`: push returns ErrProtected, main unchanged; expect `errors.Is(err, ErrProtected)`, one push only, text mentions the ruleset and `ard setup --check`, log has `reason=protected`.
  - `TestProtectedBranchLaneNoRetry`: same on a branch lane (`Branches.CommitCode`); one push only.
  - `TestProtectedRaceBudget`: main moves on every push; expect failure after `maxPushes` pushes with ErrProtected.
  - `TestMixedBudgetsIndependent`: alternate transient and protected-race failures (e.g. 3 transients and 2 races) and succeed; and 5 transients plus 3 races still succeed.
  - Edge: the re-fetch after GH006 fails transiently, which goes down the transient path.
- `internal/closeout`: a commit error wrapping `committer.ErrProtected` gives a failed plan with `CauseProtected`, not a retry.

### Risk
Medium. The change touches the retry loop that every main-lane close-out uses, and redefines what a protection failure means, which the lead flagged for a maintainer to confirm. No protected path from `merge.protected_paths` is touched (`docs/proposal.md` and Go files only). No public interface changes beyond new exported errors and a cause kind. No migrations. Security: retries are bounded by `maxPushes`, and a real protection failure still fails at once. Concurrency: the re-fetch runs inside the lane's semaphore, like the existing fetch.

### Size
M (roughly 250 changed lines including tests). Files: `internal/gitx/gitx.go`, `internal/gitx/write.go`, a gitx test file, `internal/effects/effects.go`, `internal/committer/errors.go`, `internal/committer/committer.go`, `internal/committer/protected_test.go` (new), `internal/closeout/closeout.go` and its test, `internal/worker/run.go`, `docs/proposal.md`.
