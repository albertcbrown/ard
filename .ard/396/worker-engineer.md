---
role: engineer
status: done
attempt: 1
---
Adds owner-set priority labels `ard:p0`..`ard:p3`. Within a pool class, candidates now rank by priority, then oldest; the merger orders by priority, then PR number. Priority never crosses classes.

## Changes
- `internal/labels`: the four labels in `Static()` with descriptions, `Priority` (unlabelled is P2, several use the highest), `IsPriority`; catalogue golden and count tests updated.
- `internal/pool`: `Candidate.Priority`, and `less()` orders class, priority, Since, number, role. A grant no longer clears the starvation flag; it clears only when no wait exceeds the threshold. The warning now logs key and priority. Stale `jobs.priority` comment fixed.
- `internal/github/query` and the fake: `AnyOf` (comma-form OR label filter); the fake understands it.
- Triage, worker, reviewer: set `Priority` on offered candidates and run one extra P0/P1 search per role, merged without duplicates. Reviewers take the highest label across the PR and its issue. Dry-run `would_claim` lines carry `priority` and are sorted in rank order.
- Scanner grouping and stale closer ignore priority labels. Scanner runs offer P2.
- Merger: sorts by (priority, number) from one issue search plus PR labels, so merges and the one branch update per tick follow that order.
- `docs/proposal.md`: labels, pool paragraph, merger, stale closer and grouping notes, and the reviewer visibility gap.

## Testing
`go test ./...` and `go vet ./...` pass; `gofmt -l` is clean on changed files. New tests: `labels.Priority` and static/intake checks, `less`, reservation and starvation (pool), the `AnyOf` query, per-loop priority extraction, a P0 beyond the oldest 50 for triage and worker, reviewer PR-vs-issue labels, scanner joinable and stale cases, and merger order.

## Open issues
- Adopted slots after a restart keep priority 0 and are not ranked, as before.
- Dry-run `would_claim` ordering has no test.
- The merger's priority search reads at most 100 labelled issues. Beyond that, the extras count as P2 or the PR's own label.
- CLAUDE.md was not touched.
