# Plan for #42

## Implementation

### Steps

Current state: `(*job).reviewers` in `internal/worker/run.go` takes `jb.role.Reviewers` or every reviewer role, then appends `closeout.Deferred` review requests. `config.When{Paths, Triage}` (`internal/config/types.go`) is parsed and validated in `applyOverride` (`internal/config/load.go`) but never evaluated. Matching helper `matchProtected` and `normaliseProtectedPath` live in `internal/config/protected.go`. The owner's amendments (issue body) are folded in below.

1. `internal/config/load.go`, `applyOverride` (when block): (a) normalise each `when.paths` entry with `normaliseProtectedPath`, not `normaliseRelPath`, so a trailing `/` survives, globs are validated, keep the `%s.when.paths[%d]` error key; (b) reject a non-nil rule with no paths and no triage: "when must set paths or triage; remove when: to run on every PR" (`when: null` stays "always"). Check `render.go` output and the `go.mod,package*.json` assertion in `load_test.go` still hold.
2. `internal/config/types.go`: replace "Semantics are owned by #15" on `When` with a pointer to `Matches`. Add `func (w *When) Matches(changed []string, triageRan func(role string) bool) bool`: nil matches; else (any changed path matches any `Paths` entry via `matchProtected`) OR (any `Triage` role has `triageRan` true). Doc comment pins: case-sensitive, anchored at repo root; entry ending `/` is a directory on a segment boundary; otherwise doublestar glob (`*` within a segment, `**` crosses, `**/go.mod` matches root `go.mod`); slash-free glob matches only at root.
3. `internal/config` (new file `select.go`): `func (r *Resolved) SelectReviewers(worker Role, changed []string, triageRan func(string) bool) []string`. Candidates are `worker.Reviewers` when non-empty (only existing reviewer roles), else every reviewer role, in config order; keep a candidate whose `When.Matches` is true. Returns the base set only. A worker `reviewers:` list narrows candidates and never bypasses `when:`.
4. `internal/gitx/local.go`, `ChangedPaths`: add `--no-renames` to the diff args so a rename lists old and new paths and deletions are listed. Update the doc comment. Run committer tests (the declared-path check becomes stricter).
5. `internal/closeout/closeout.go` (new): `func TriageRoles(ctx, s, issue int) (map[string]bool, error)` modelled on `Deferred`: `CloseoutsOf(ctx, issue, StageTriage)`, keep `Phase == PhaseClosed` and `Plan.Outcome == OutcomeDone` (needs-human and failed excluded), return the set of `Job.Role`.
6. `internal/worker/loop.go`: add `ChangedPaths(ctx, dir, from, to string, paths ...string) ([]string, error)` to the `Git` interface (`*gitx.Git` already has it); update the fake in `internal/worker/harness_test.go`.
7. `internal/worker/run.go`:
   - Add `head string` to `job`. In `push()` set `jb.head = res.SHA`; in `round`'s no-code path use `s.h.BaseSHA`. `finishWithPR` reaches `openPR`, so it uses `jb.head` as well.
   - Rewrite `reviewers` doc comment (state monotonic selection: each run recomputes from the whole PR diff, close-out only adds labels, so earlier requests stay and newly matching reviewers are added) and body:
     - `head` empty or all zeros: skip git, fail open (every role with non-empty `Paths` matched), no warning.
     - Otherwise `base, err := MergeBase(RepoDir, jb.cfg.SHA, head)`, then `ChangedPaths(RepoDir, base, head)`. On error log one `worker.reviewers_failed` with `step` = `merge_base` or `changed_paths` and fail open for path rules.
     - Drop every path under `.ard/` and `.ard-input/` before matching.
     - `triageRan` = union of `closeout.TriageRoles` and `labels.Done(labels.StageTriage, role)` present in `jb.is.Labels`. On store error log `worker.reviewers_failed` `step=triage` and use labels alone.
     - Call `jb.cfg.SelectReviewers(jb.role, changed, triageRan)`, then append lead `closeout.Deferred` review requests and the worker role file's own `next` review requests (added in `buildWorker`) unconditionally; keep existence check, de-dup, stable order.
     - If reviewer roles exist and the final set is empty, log `worker.no_reviewers` at Warn with issue and PR. Gates are unchanged (`prgate` returns `ReasonNoReviews`, PR stays draft).
   - Tests failing already skips requests in `buildWorker`; do not evaluate `Matches` then.
8. `internal/configcmd/config.go`, `Check`: when reviewer roles exist and every one has a `When`, print a warning to `e.Stderr` (not an error, exit code unchanged) that a PR matching none stays a draft.
9. Docs: `docs/proposal.md` step 8 (~line 305): drop "`when:` rules are ignored until #42" and state paths OR triage, worker list narrows, `next` bypasses, changed-path range (merge base to head), `.ard/` not selectable, `done`-only triage, monotonic selection, empty selection leaves a draft, fail-open. Reviewer loop (~339) and Configuration example (~400): `**/package*.json`, `**/go.mod`, add a `web/` example; document the `config check` warning. Update `internal/roles/config.yaml` comments if they mention `when:`.

### Test plan

Run `go test ./internal/config/... ./internal/gitx/... ./internal/committer/... ./internal/closeout/... ./internal/worker/... ./internal/configcmd/...`, then `make test`.

- `internal/config/load_test.go`: `TestLoadWhenKeepsDirSlash` (`when: {paths: [web/, "**/*.lock"]}` gives `["web/", "**/*.lock"]`); `TestLoadWhenInvalidGlob` rejected with the `when.paths[i]` key; `TestLoadWhenEmptyRejected` (`when: {}` and `when: {paths: []}` fail; `when: null` loads as nil).
- `internal/config` new `when_test.go`, table for `Matches`: nil true; `web/` vs `web/a/b.tsx` true, `web` true, `webapp/x` false; `**/go.mod` vs `go.mod` true, `a/b/go.mod` true, `Go.mod` false; `package*.json` vs `package.json` true, `sub/package.json` false; `go.mod` exact glob (rename of old `TestWhenPathsDirPrefix`); `**/*.lock`; triage ran true/false; paths OR triage; empty changed with paths-only rule false; #44's exact starter list as a row.
- `internal/config` `TestSelectReviewers`: no `when` selects all; path hit/miss; triage hit; worker list narrows and still honours `when:`; order is config order; unknown names ignored.
- `internal/gitx` `TestChangedPathsRenames` (rename lists both paths; deletion listed).
- `internal/closeout` `TestTriageRolesOnlyClosedDone` (closed done counted; failed, open, needs-human ignored; duplicates collapsed).
- `internal/worker/loop_test.go` (extend `TestReviewersUnsetMeansAll`): when-gated reviewer skipped and included; triage via state only and via label only; needs-human triage does not count; worker list does not bypass `when:`; lead-deferred and role-file `next` bypass `when:` (no duplicates); new branch with no `refs/ard/` ref works; dry-run zero SHA fails open without warning; git error fails open with one `worker.reviewers_failed` (`step`); store error falls back to labels; `.ard/` only diff selects nothing path-gated; empty selection logs `worker.no_reviewers`; monotonic: run 1 without `go.mod` skips license-review, run 2 on the same branch adds `go.mod` and requests it, eng-review not duplicated; `Matches` not consulted when tests failed.
- `internal/configcmd/config_test.go`: warning when every reviewer has `when:`; none otherwise.

### Risk

Medium. Reviewer selection is a review-gate decision, so wrong matching could weaken coverage; it fails open and explicit `next` requests always win. The `--no-renames` change to shared `gitx.ChangedPaths` affects the committer's declared-path check (stricter). `docs/proposal.md` and `internal/**` are not in `merge.protected_paths`; if `internal/roles/config.yaml` or any `.ard/` file needs editing it would need a human. No migration or concurrency change; `Git` interface gains one method already implemented.

### Size

L (about 400 to 600 changed lines, most of them tests). Files: `internal/config/load.go`, `types.go`, `select.go` (new), `load_test.go`, `when_test.go` (new), `internal/gitx/local.go` and its test, `internal/closeout/closeout.go` and test, `internal/worker/loop.go`, `run.go`, `harness_test.go`, `loop_test.go`, `internal/configcmd/config.go` and test, `docs/proposal.md`, possibly `internal/roles/config.yaml`.
