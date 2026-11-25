# Plan for #396

## Implementation

### Steps
Two PRs are proposed in the issue; a single worker run may do part 1 and part 2 in order, each step leaving the build green. Do not touch CLAUDE.md (protected; the invariant wording already landed).

1. `internal/labels/labels.go`: add consts `P0..P3 = "ard:p0".."ard:p3"` (new) to the flat label block; append them to `Static()` (update the "nine" doc comment). Add `func Priority(ls []string) int` (new): returns 0..3, highest (lowest number) wins, case-insensitive, unlabelled returns 2. Add `func IsPriority(l string) bool` (new) for the scanner exemptions. Check `Parse` and `CheckIntake` treat them as static (they should via `Static()`).
2. `internal/labels/colors.go`: add a style and description for each (e.g. "ard priority 0 (highest); unlabelled counts as P2") so `Desired` and `ard labels sync` create them. Keep descriptions under the length limit in `catalogue.go`. Update any test that counts the static labels or checks `literal_test.go`.
3. `internal/pool/candidate.go`: add `Priority int` to `Candidate` (default zero would mean P0, so document that callers set it, with `labels.Priority` giving P2 for none; scanners pass 2). `less()`: class, then Priority, then Since, Number, Role. Do not import `labels` (pool stays stdlib-only). Fix the stale `jobs.priority` comment at `internal/pool/class.go:11-12`.
4. `internal/pool/pool.go` (grant path, ~line 245) and `internal/pool/demand.go` `checkStarvationLocked`: stop clearing `starved[class]` on grant; clear it only when the class has no wait older than `starveAfter` (or none waiting). Log `key` and `priority` of the oldest waiter in `pool.starved`. `foreignLocked` already uses `less`, so no change for reservations.
5. `internal/github/query/query.go`: add `AnyOf []string` to `RoleQuery` (new) rendered as `label:"a","b"`-style OR (GitHub comma form `label:ard:p0,ard:p1`), validated with `check`. Table test in `query_test.go`.
6. `internal/triage/candidates.go` and `internal/worker/candidates.go`: after the per-role search (`Max: 50`), run a second search with `AnyOf: {labels.P0, labels.P1}` and merge hits, de-duplicating by number (keep `busy` handling). Set a `priority` on `candidate` from `labels.Priority(is.Labels)` in `consider`. Pass `Priority` in the `pool.Candidate` built in the loops (`triage/loop.go` ~line 207; the worker loop equivalent). Fix rounds inherit via the worker candidate.
7. `internal/triage/loop.go` (~202-208): in dry run sort `cands` by (priority, Since, number) and add a `priority` attribute to `triage.would_claim`; do the same in the worker and reviewer dry-run paths where they log would-claim lines.
8. `internal/reviewer/candidates.go` (`consider`, ~line 123 onward): `Priority = min(labels.Priority(is.Labels), labels.Priority(pr.Labels))`; add the extra P0/P1 search on PRs; pass `Priority` into `pool.Candidate`. The documented gap: a P0 on the issue only is not found beyond the oldest 50.
9. Scanner: `internal/scanner/pipeline.go` (~544, joinable label check) skip `labels.IsPriority` labels in the loop; `internal/scanner/stale/decide.go` (~61-66) likewise. Scanner pool candidates pass `Priority: 2`.
10. Merger (part 2) `internal/merger/loop.go` `candidates` (~line 232): replace the sort by number with (priority, number). Priority = highest of the PR's labels and the linked issue's, taken from one search per tick for open issues labelled `ard:p0,ard:p1,ard:p3` (map issue number to priority, default 2). Use the same order for the one branch update per tick. The issue number comes from `parseBranch`.
11. `docs/proposal.md`: add the four labels to the label list/table (~line 83-85), the "One pool of job slots" paragraph (priority within a class, never across), the merger section, and the stale-closer (~227) and scanner-grouping notes; state the reviewer visibility gap. Remove any other mention of nine labels.

### Test plan
Run `make lint test` (the worker's configured command). No network; use the existing fakes.
- `internal/labels`: `TestPriority` table: none gives 2; `ard:p1` gives 1; `ard:p3,ard:p0` gives 0; mixed case; unrelated labels. `TestStaticIncludesPriority`, catalogue/Desired includes the four with descriptions; `CheckIntake("ard:p0")` is rejected; sync idempotent and `--dry-run` only logs (extend the existing sync tests).
- `internal/pool`: `TestLessPriority` table: P1 before older P3 in a class; class still first (P3 review before P0 triage); ties fall to Since, Number, Role; order independent of input order. Reservation test: a waiting P0 holds the reservation ahead of an older P3 in the same class. Starvation test: one `pool.starved` warning per episode across several grants, with key and priority attributes; re-arms after the class empties.
- `internal/github/query`: `AnyOf` rendering and invalid label.
- `internal/triage`, `internal/worker`, `internal/reviewer`: with one free slot, a P1 candidate starts before an older P3; a P0 outside the oldest 50 is found by the extra search and ranked first (fake returns it only for the AnyOf query); duplicates removed; PR label P0 on a P2 issue ranks P0; relabel between two ticks changes the order; dry-run `would_claim` lines carry `priority` and are in rank order.
- `internal/scanner`, `internal/scanner/stale`: an issue with `ard:p0` is still joinable and still stale-closable.
- `internal/merger`: a P0 PR merges and gets its branch updated before an older P2 PR; PR-label vs issue-label highest wins; unlabelled is P2.
- Edge cases: empty label lists, issue lookup failure in the merger (fall back to P2 and log), a search error on the extra query (log, continue with the main results).

### Risk
Medium. Central ordering in the shared pool and the merger changes which PRs merge first (security-adjacent only through ordering, the merge gates are unchanged). No migration or persisted state. Zero-value `Priority` meaning P0 is a pitfall: every constructor of `pool.Candidate` must set it (grep for `pool.Candidate{`, including tests and scanners). No protected paths from `merge.protected_paths` are touched (docs/proposal.md is not protected); CLAUDE.md must not be edited. Concurrency: only the starvation flag logic runs under the pool mutex already held. Plan assumes the slot wake-up change (#394) is on main; the lead reports it landed.

### Size
L, about 850-950 changed lines. Under the 3,000 line limit, so a single run is allowed, though a maintainer may prefer two PRs (steps 1-9 and 11, then 4 and 10).
Files: internal/labels/{labels,colors}.go and tests, internal/pool/{candidate,class,pool,demand}.go and tests, internal/github/query/query.go and test, internal/triage/{candidates,loop}.go, internal/worker/candidates.go and its loop, internal/reviewer/candidates.go, internal/scanner/pipeline.go, internal/scanner/stale/decide.go, internal/merger/loop.go, docs/proposal.md, plus tests.
