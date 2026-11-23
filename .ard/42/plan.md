# Plan for #42

## Implementation

### Steps

Current state: `(*job).reviewers` in `internal/worker/run.go` already implements the worker `reviewers:` override (`jb.role.Reviewers`, else every reviewer role) and adds lead-deferred reviewers via `closeout.Deferred`. Config already parses and validates `Role.When` (`config.When{Paths, Triage}` in `internal/config/types.go`). Nothing evaluates `When`; that is the gap.

1. `internal/config/types.go` (new method, no behaviour change elsewhere): add `func (w *When) Matches(changed []string, triageRan func(role string) bool) bool`. A nil `w` returns true. Otherwise the rule passes when any changed path matches any of `w.Paths` or any of `w.Triage` roles ran (paths OR triage, as the proposal's "paths, or a triage role that ran"). Reuse `matchProtected` from `internal/config/protected.go` (directory prefix ending in `/`, else doublestar glob), so path rules follow the same syntax as `merge.protected_paths`. Empty `Paths` and empty `Triage` on a non-nil `When` match nothing; confirm in `parseWhen` (`internal/config/load.go`) that an empty rule is rejected, and if not keep it as "never runs".
2. `internal/closeout/closeout.go` (new): `func TriageRoles(ctx, s, issue int) (map[string]bool, error)` modelled on `Deferred`: reads `CloseoutsOf(ctx, issue, StageTriage)`, decodes each, keeps those with `Phase == PhaseClosed` and `Plan.Outcome == OutcomeDone`, and returns the set of `Job.Role`.
3. `internal/worker/loop.go`: add `ChangedPaths(ctx context.Context, dir, from, to string, paths ...string) ([]string, error)` to the `Git` interface (already implemented by `*gitx.Git`, `internal/gitx/local.go`); update the fake in `internal/worker/harness_test.go`.
4. `internal/worker/run.go`, `(*job).reviewers`: restructure into three stages.
   - Base set: if `jb.role.Reviewers` is non-empty it is the set and `When` is NOT applied (worker override wins). Otherwise take every reviewer role whose `Role.When` matches. Changed paths come from `Git.ChangedPaths(RepoDir, mergeBase(main, branch), localRef(jb.branch))`, skipping `.ard/<issue>/` via the existing `excluded(issue, p)` in `guard.go`; triage roles come from `closeout.TriageRoles`. On a git or store error log a warning (`worker.reviewers_failed`) and fail open: run the reviewer rather than skip it.
   - Then append `closeout.Deferred` reviewers (lead `next`), unconditionally, so a lead request overrides a `when:` rule.
   - Keep the existing existence check and de-duplication, order stable (config order, then deferred).
5. `docs/proposal.md` (Reviewer loop, last paragraph before the table, and the Configuration section for `when:`): state the exact semantics: paths OR triage; paths exclude the issue's own `.ard/<issue#>/`; a worker `reviewers:` list bypasses `when:`; lead `next` always adds; evaluation fails open. CLAUDE.md requires proposal and code to change together.

### Test plan

Run `go test ./internal/config/... ./internal/closeout/... ./internal/worker/...` and then `make test`.

- `internal/config/load_test.go` (or new `when_test.go`): `TestWhenNilAlwaysRuns`; `TestWhenPathsDirPrefix` (`go.mod` rule, changed `["go.mod"]` true, `["main.go"]` false); `TestWhenPathsGlob` (`**/*.lock`); `TestWhenTriageRan` (triage role ran true, did not run false); `TestWhenPathsOrTriage` (path misses, triage hits gives true); `TestWhenEmptyChanged` (no changed paths with a paths-only rule gives false).
- `internal/closeout/closeout_test.go`: `TestTriageRolesOnlyClosedDone` (closed done counted; failed, open or needs-human close-outs ignored; duplicates collapsed).
- `internal/worker/loop_test.go`, extending `TestReviewersUnsetMeansAll` (currently expects all three): `TestReviewersNoWhenRunsAll` (unchanged); `TestReviewersWhenPathsSkips` (license-review with a paths rule and a diff not touching it is omitted); `TestReviewersWhenPathsIncludes`; `TestReviewersWhenTriageRole`; `TestReviewersWorkerOverrideBypassesWhen` (role `Reviewers` set, a when-gated reviewer listed still runs, unlisted ones do not); `TestReviewersLeadNextAddsGated` (deferred review request for a when-gated reviewer that does not match is added; unknown role ignored; no duplicate); `TestReviewersWhenGitErrorFailsOpen`.
- Edge cases: a diff of only `.ard/<issue#>/` files; a deleted file counts as changed; a path with spaces or non-ASCII (use `-z`, already in `ChangedPaths`).

### Risk

Low to medium. No protected paths are touched (`docs/proposal.md`, `internal/**` are not in `merge.protected_paths`); no migration, no new config keys, and the `Git` interface only gains a method that `*gitx.Git` already has. The risk is behavioural: a reviewer that used to run on every PR can now be skipped, which is why evaluation fails open and an explicit worker or lead request always wins. Reviewer selection is a review-gate decision, so wrong matching could weaken review coverage; the tests above pin each rule. No concurrency changes.

### Size

M (about 150 to 250 changed lines, over half of them tests). Files: `internal/config/types.go`, `internal/config/load_test.go` (or new `internal/config/when_test.go`), `internal/closeout/closeout.go`, `internal/closeout/closeout_test.go`, `internal/worker/loop.go`, `internal/worker/run.go`, `internal/worker/harness_test.go`, `internal/worker/loop_test.go`, `docs/proposal.md`.
