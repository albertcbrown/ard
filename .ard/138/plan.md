# Plan for #138

## Implementation

### Steps

Approach: rebase the job's own section onto the current `main` copy at commit time (the issue's preferred option), and fail closed to `needs-human` only when the rebase cannot be done safely. Today `closeout.checkWrites` (`internal/closeout/allow.go`) compares `plan.md` with the base commit, but `committer.Committer.Commit` (`internal/committer/committer.go`, `lane.attempt`) writes the job's whole file onto the freshly fetched head. For `.ard` requests `snapOf` sets no `snap.base`, so nothing guards this. The merge must run inside `lane.attempt`, because the committer refetches and re-applies on each push retry and the current copy can change between attempts.

1. `internal/committer/request.go`: add an optional field to `File`: `Merge func(current []byte, exists bool) ([]byte, error)`. When set, it replaces the "exactly one of Data, Src, Delete" meaning of nothing: `Data`/`Src` still supply the job's bytes, and `Merge` receives the current head bytes of that path. Carry it through `snapshot` (`out.Files[i]` keeps it, it is already copied by value) and add `merge` to `snapFile` in `internal/committer/committer.go`; set it in `snapOf`. Reason: lets the caller own plan.md semantics while the committer owns fetch/retry.
2. `internal/committer/committer.go`, `lane.attempt`: in the write loop, when `f.merge != nil && !f.del`, read the file from `l.wt` (after `ResetHard` to base; absent => `exists=false`), call `f.merge(cur, exists)`, and write the result instead of `f.data`. On error return a new exported `ErrMerge` (add to `internal/committer/errors.go`, wrapped with the path), not retryable. Reason: the worktree is exactly the latest head at this point.
3. `internal/closeout/plan_merge.go` (new): `func mergeOwnSection(staged []byte, own string) func(cur []byte, exists bool) ([]byte, error)` using existing `parsePlan`, `planDoc`, `planSection`, `others`, `trimEnd`. Behaviour: if `!exists`, return `staged` (job created the file). Otherwise take `cur`'s preamble and all non-own sections unchanged; take the own section from `staged` (zero or one; `checkPlan` already rejected duplicates); put it where `cur` has its own section, else append at the end; if staged has none, drop cur's own section. Return an error if `cur` has more than one section with `own`'s heading or a heading-like setext line in `cur`'s own section (fail closed). Keep a single trailing newline join consistent with how `parsePlan` splits (sections' `text` includes the heading line). Return `staged` unchanged when `cur` equals the job's base for the other sections (fast path, byte-identical to today).
4. `internal/closeout/closeout.go`, `Closer.commit`: for each `FileRef` whose path is `.ard/<n>/plan.md` and not `Delete`, set `cf.Merge = mergeOwnSection(<staged bytes>, section)`. `section` comes from `sectionOf(c.d.Roles, rec.Job.Stage, rec.Job.Role)` (a plan.md file only appears when this is non-empty). Read the staged bytes from `f.Staged` (resume-safe, as the code already reads only staged files). Skip when `rec.Job.Branch != ""`.
5. `internal/closeout/closeout.go`, `Closer.commit` error branch: if `errors.Is(err, committer.ErrMerge)`, replace the plan with `needsHumanPlan(rec.Job, rec.Plan.Status, "plan.md conflict: <reason>")` (keeping Tail fields as the existing failure branch does) instead of `failedPlan`, so the issue gets `needs-human` with a conflict reason and only the log is committed. Check that `Build`/Prepare flow leaves Files in a state where only the log is committed; if the log file is already in `p.Files`, drop all files except it and the role file as the existing allowlist-denial path in `prepare.go` does (reuse that helper if one exists).
6. `docs/proposal.md`, "Who may write what" (about line 116): add a paragraph: close-out checks `plan.md` against the job's base commit, and when it commits, the committer replaces only the job's own `##` section in the current `main` copy, so a section another role committed after the job's base is kept; if the current file cannot be merged (duplicate own heading), the job fails closed as `needs-human` with a conflict reason.

### Test plan

Run `go test ./internal/closeout/... ./internal/committer/...`, then `make test` and `make lint`.

- `internal/closeout/plan_merge_test.go` (new), table test of `mergeOwnSection`:
  - `other_section_changed_on_main`: staged has `## Requirements` (old) + `## Implementation` (new); cur has `## Requirements` (newer). Result keeps newer Requirements and new Implementation.
  - `own_absent_in_cur`: own section appended at end.
  - `own_removed_by_job`: staged lacks own; cur's own is dropped, others kept.
  - `cur_missing`: `exists=false` returns staged bytes.
  - `cur_unchanged`: result equals staged bytes.
  - `fenced_heading`: a `## X` line inside a code fence in another section is preserved untouched.
  - `duplicate_own_in_cur`: returns an error.
- `internal/committer/committer_test.go`: `TestCommitMergeSeesLatestHead` (file remote as in existing tests): push a change to the path after the request is built, Merge gets the new bytes and its result is committed; `TestCommitMergeError` returns `ErrMerge` with no commit and no push; a Merge-less file behaves as before.
- `internal/closeout/closer_test.go` (uses `env_test.go` harness): `TestCommitKeepsNewerSectionOnMain`: job base has `## Requirements` v1; main is advanced with Requirements v2 before commit; the engineer job's staged plan.md holds v1 plus `## Implementation`. After close-out, main's plan.md has Requirements v2 and the job's Implementation. This is the regression test the issue asks for. Also `TestCommitPlanConflictNeedsHuman`: duplicate own heading on main gives outcome `needs-human`, reason mentions plan.md, and plan.md is not committed.
- Edge cases: concurrent push retry (second attempt re-merges against the newer head); plan.md absent on main; job with no plan.md change (no Merge set).

### Risk

Medium. It changes `committer.File` and `lane.attempt`, a shared public interface used by the scanner, intake and controller. The new field is optional, so existing callers are unchanged. It touches concurrency-adjacent retry logic and the text-merge logic of a file agents write. No protected paths from `merge.protected_paths` are modified (only `internal/` and `docs/`). The `.ard/` files that agents read are not affected. Not a migration or secret-handling change. A design confirmation by a maintainer is worthwhile, as the lead noted.

### Size

M (about 200 to 280 changed lines including tests). Files: `internal/committer/request.go`, `internal/committer/committer.go`, `internal/committer/errors.go`, `internal/committer/committer_test.go`, `internal/closeout/plan_merge.go` (new), `internal/closeout/plan_merge_test.go` (new), `internal/closeout/closeout.go`, `internal/closeout/closer_test.go`, `docs/proposal.md`.
