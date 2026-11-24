---
role: engineer
status: done
attempt: 1
---
Adds the `prwatch` loop, which watches ard's own pull requests every `intervals.prwatch` (default 5m). It records human merges, flags PRs closed without merging, long-pending required checks and red drafts, and never merges or updates anything. Check evaluation moves from the merger into `internal/prgate`, so both loops judge checks with one function.

## Changes
- `internal/prgate/checks.go`: `EvalChecks`, `CheckState` and the helpers moved unchanged from the merger. New `ResolveApps` resolves status creators to App ids for pinned rules. The merger's check tests moved to `checks_test.go` unchanged, plus a #256 regression case.
- `internal/merger`: calls `prgate.EvalChecks`; the moved code is deleted.
- `internal/config`: `intervals.prwatch` (default 5m, min 1m), `pr_watch.pending_timeout` (default 2h, at least the interval, at most 7d), `ard config check` output, tests and golden.
- `internal/state`: migration 0008 (`pr_watch` table), row store methods, `EventPRWatch`, schema golden.
- `internal/github`: `PullRequest.MergedBy`, filled by `GetPR` only (GitHub omits it from lists). The fake models this and records the merger's actor.
- `internal/prwatch` (new): loop, actions, idempotence (row flag, event, marker comment), sanitizing, dry-run, and tests.
- `internal/servecmd`: registered after the merger and added to `knownLoops`; tests updated.
- `docs/proposal.md` (new "PR watch loop" section, config block, `needs-human` source, merger pointer) and `docs/layout.md`. CLAUDE.md is untouched, per the owner's comment.

## Testing
`make test` passes. `go vet ./...` is clean. `gofmt -l .` flags only `internal/roles/testdata/docs/skips/src/_x.go`, a file I did not touch. New table-driven tests use the GitHub fake with no network, covering each acceptance criterion: restart idempotence, the check-call counts, dry-run, busy skip, error isolation and effect-failure retry.

## Open issues
- prwatch acts only on PRs it has seen open. A PR closed or merged before the loop first saw it is ignored, so installing it does not replay history. A PR opened and merged between two ticks is also missed.
- Marker comments include `pr=<n>` (the status issue is shared by all PRs), so they differ slightly from the form in the issue.
- A human merge event has no `merge_sha`: GitHub's PR read does not give it cheaply.
- `AppID` lookups for pinned rules are cached per process and are not counted in the 1+2N bound.
