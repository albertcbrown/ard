# Plan for #221

## Implementation

### Steps
1. **New `internal/scanner/streak.go`**: pure streak logic, no I/O.
   - `type streakClass string`; `classOf(res RunResult) (class string, failing, neutral bool)`:
     - `OutcomeBackpressure` -> `max_open`, failing.
     - `OutcomeScanFailed` with `res.GuardRule != ""` -> class is `res.GuardRule` (rule name, or `scan-error` when a guard pass failed), failing. Never use `GuardField`, finding text or `ScanError` text.
     - `OutcomeScanFailed` without a guard rule, and `OutcomeTimeout` -> `scan_failed`, failing.
     - `OutcomeOK` -> recovered.
     - everything else (`OutcomeGlobalCap`, `OutcomeError`, `OutcomeNoSlot`, `OutcomePaused`, `OutcomeSkipped`) -> neutral: streak unchanged, no post.
   - Sanitise the class to `[A-Za-z0-9._:-]`, max 64 chars, so a rule name can carry no markup, `@` or marker.
   - `notice(scanner, class string) string` and `recovery(scanner string) string` build the comment bodies. The body holds the scanner name and class only, plus a fixed marker-free sentence. Scanner name is config-controlled (max 38 chars).
2. **Streak state in the scanner DB, no schema change**: the existing `meta` table (`scannerdb.DB.Meta/SetMeta`, migration 0001) holds key `failure_streak` = the posted class, absent or empty = no open streak. Existing DB files load unchanged (key absent = no streak), so no migration. Add `Meta(ctx,key)` and `SetMeta(ctx,key,value)` to the `Store` interface in `internal/scanner/store.go` (`*scannerdb.DB` already satisfies them). Update the fakes in `internal/scanner/harness_test.go` that implement `Store`. Treat `scannerdb.ErrNotFound` as "no streak".
3. **Notice sink on `Pipeline`** (`internal/scanner/pipeline.go`): add field `StatusIssue func() int` (status issue number; <=0 when unknown) next to `Paused`. Posting uses the existing `Effects.CreateComment(ctx, n, body)`, so the effect layer logs it under `--dry-run` instead of calling GitHub. Add `func (p *Pipeline) noteRun(ctx, store Store, role config.Role, res RunResult)`:
   - read `failure_streak`; compute `classOf`; neutral -> return.
   - failing and stored empty -> post notice, then `SetMeta`. Failing and stored class differs: a streak is one per scanner per issue wording ("one comment per failure streak"), so do not repost on a class change; keep the stored class. Failing and same -> nothing.
   - recovered and stored non-empty -> post recovery, then clear (`SetMeta(..., "")`). Recovered with none stored -> nothing.
   - If `StatusIssue` is nil or <=0 (not yet known, or dry-run synthetic): log `scanner.streak_unposted` and leave state unchanged, so it posts once the issue exists. Under dry-run (`p.Effects.DryRun()`) the issue number is negative, so also log the notice at info (`scanner.streak_notice` with scanner and class) and do NOT write the state key, so dry-run never claims a streak that was not posted. State is written only after a successful post; a post error is logged and returns without the state write (retried next run, at-least-once; an unwritten state write after a successful post at worst reposts once).
   - All errors are logged, never returned: a notice failure must not change the run's result.
4. **Call `noteRun` from `record`** in `Pipeline.Run` (`internal/scanner/pipeline.go`, the `record` closure), after `RecordRun`, so every recorded outcome is seen exactly once. `res.GuardRule` is already set before `record` on all guard paths. The closeOnly path with `OutcomeScanFailed` is covered by the same hook. Unrecorded outcomes (no slot, paused) never reach it.
5. **Wire it** in `internal/servecmd/scanners.go`: set `StatusIssue: func() int { return int(w.statusIssue.Load()) }` on the `scanner.Pipeline` literal (same source `cfgLoader` uses in `serve.go:216`).
6. **Docs**: in `docs/proposal.md` add to the Observability bullet (and the status-issue text in the Kill switch bullet if it fits) a sentence: each scanner posts one comment on the status issue when a failure streak starts (guard rule name, `scan_failed` or `max_open`) and one when it recovers; the streak is kept in the scanner's `meta` table (`failure_streak`), so a restart does not repost; comments carry the scanner name and reason class only; dry-run logs instead of posting; `timeout` counts as `scan_failed`; global-cap drops and `error` runs neither start nor end a streak. Mention this is the early slice of the digest (#58). Tick the TODO entry in `docs/TODO.md` if one exists for #221.

### Test plan
Command: `make test` (also `go test ./internal/scanner/... ./internal/servecmd/...`; `make lint`).
- `internal/scanner/streak_test.go`:
  - `TestClassOf` table-driven: backpressure -> `max_open`; scan_failed + GuardRule `aws-access-token` -> that rule; scan_failed + `scan-error` -> `scan-error`; plain scan_failed -> `scan_failed`; timeout -> `scan_failed`; ok -> recovered; global cap, error, no-slot, paused, skipped -> neutral.
  - `TestStreakSanitise`: a rule name with newline, `<!--`, `@user` yields a clean class.
- `internal/scanner/pipeline_streak_test.go` (use the harness in `harness_test.go` and the guard setup in `pipeline_guard_test.go`):
  - `TestStreakPostsOncePerStreak` table-driven over each class: 3 failing runs -> exactly 1 comment on the status issue, then an ok run -> exactly 1 recovery comment, then an ok run -> none.
  - `TestStreakSurvivesRestart`: fail run, build a new `Pipeline` over the same store, fail again -> still 1 comment; recover on the new pipeline -> recovery posted.
  - `TestStreakNeutralOutcomes`: global-cap/error runs in the middle of a streak do not repost or recover.
  - `TestStreakNoFindingText`: plant marker `PLANTED-SECRET-MARKER-123` in a finding title/detail/fingerprint and in the scan error output; assert no posted body contains it, and that the body contains the scanner name and class.
  - `TestStreakDryRun`: dry-run effects: no CreateComment on the fake GitHub, notice logged, state key not written.
  - `TestStreakNoStatusIssue`: `StatusIssue` nil or 0 -> no post, state untouched; once set, next failing run posts.
  - `TestStreakPostFailure`: CreateComment error -> run result unchanged, state not set, next failing run retries.
  - `TestStreakOldStateLoads`: DB without the key behaves as no streak.
- Servecmd: a wiring test in `internal/servecmd/scanners_test.go` that `StatusIssue` is set.

### Risk
Low to medium. No schema migration (existing `meta` table, absent key = no streak). The `Store` interface gains two methods, so test fakes need updating. Touches the scanner pipeline's hot path, but the hook only reads and writes one meta key and never changes the run result. Security-sensitive: the post text must never include finding data; the body is built from a sanitised class and the scanner name only, with a test. No protected paths from `merge.protected_paths` are touched (`docs/` and `internal/` only). No concurrency change: scanners run one per tick, and state is per scanner.

### Size
M (about 250 changed lines including tests). Files: `internal/scanner/streak.go` (new), `internal/scanner/store.go`, `internal/scanner/pipeline.go`, `internal/scanner/harness_test.go`, `internal/scanner/streak_test.go` (new), `internal/scanner/pipeline_streak_test.go` (new), `internal/servecmd/scanners.go`, `internal/servecmd/scanners_test.go`, `docs/proposal.md`, possibly `docs/TODO.md`.
