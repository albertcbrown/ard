---
role: engineer
status: done
attempt: 1
---
Gates `agent: claude` jobs on the 5-hour thresholds, the paced weekly caps and a one-agent-job rule above the 5-hour stop-workers threshold, so a job does not start as the subscription nears a limit.

## Changes
- `internal/config`: new `limits:` block (`stop_workers_pct` 75, `stop_all_pct` 90, `weekly_stop_workers_pct_by_day` `[20,35,50,60,70,80,85]`, `weekly_stop_all_pct` 90) with validation, keys and render output (`limits.go`, golden file).
- `internal/limits/policy.go`: `Policy`, `Tier`, `Book.Gate` and `WeeklyDay`. Utilization may be a fraction or a percent. A reading past its reset time is ignored. With no `resets_at` the day-7 value applies.
- `internal/pool`: `Candidate.Agent` and `Candidate.Serial`, `Options.Annotate` (also applied to held jobs) and a `DeniedSerial` verdict. A Serial candidate is refused while another Agent job is held or granted earlier in the batch.
- `internal/servecmd`: `limitedFor` joins the reactive per-family pause with the gate for claude roles, and `annotate` is passed to the pool. Deterministic scanners, the merger and non-claude roles are untouched.
- Docs: `docs/proposal.md` (Executors paragraph and `limits:` config) and `docs/site/configuration.md` key table.

## Testing
`make lint test`: everything passes except `docscheck`, which first failed because the new keys were missing from `docs/site/configuration.md`. I added them and reran `go test ./internal/docscheck`, which passes. I did not rerun the full suite after that docs-only fix. The `go vet` line about `internal/roles/testdata/.../_x.go` is a deliberate fixture and is not from this change.
Added tests: config defaults and rejections (6 or 8 values, 0, 101, decreasing, above weekly stop-all, non-integer, workers above all, unknown key); a `limits` Gate table (74/75/90%, weekly day 1/6/7 boundaries, custom list, no `resets_at`, stale readings, fraction form); pool serial tests; servecmd threshold and Opus-only fixtures.

## Open issues
- No `limits` loop with `Claims() == false` exists, so none was added. Gating goes through the pool's Limited and Annotate hooks.
- `adoptPool` takes `Annotate` as an optional variadic argument to keep existing callers unchanged.
- The owner wants to review by hand before merge. `internal/pool` is not a protected path, so a maintainer must hold the PR.
