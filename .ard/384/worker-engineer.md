---
role: engineer
status: done
attempt: 1
---
Adds a `needs-network` worker status with a strictly validated `network:` list (what, url, why, optional verify). The close-out ends as needs-human, lists the requests in the issue comment, opens no PR and pushes nothing. The back-route comes in a later part.

## Changes
- `internal/closeout/rolefile.go`: new status, `NetworkNeed`, `networkOf` validation (1–10 entries, https URLs, one-line capped text), type check, and worker-only rule.
- `internal/closeout/plan.go`, `prepare.go`, `comment.go`: `OutcomeNeedsNetwork`, `Plan.Network`, use of the redacted re-parse (needs-human if the list is lost), and comment rendering.
- `internal/costs/rows.go`, `internal/reaper/reaper.go`, `internal/reaper/finish.go`: handle the new outcome.
- `docs/proposal.md`: documents the status and the interim ending.
- Tests in `rolefile_test.go`, `plan_test.go`, `prepare_test.go`, `comment_test.go` and `closer_test.go`.
- `internal/closeout/closeout.go`: re-wrapped the record-version comment.

## Testing
Ran `go test ./internal/closeout/`: passes. Earlier rounds ran the costs and reaper packages.

## Review findings
- `internal/closeout/closer_test.go`: fixed: added `TestRunWorkerNeedsNetwork`, a Run-level test. It checks the outcome, one comment with the URL, `ard:needs-human` added, `:running` removed, no review labels requested or added on the PR, and that only `.ard/` files are committed (the report and cost record). It does not check pushing or PR creation directly, because the Run-level helpers don't model them.
- `internal/closeout/closeout.go` line 24: fixed: comment re-wrapped.

## Open issues
- `make lint` fails on a pre-existing testdata fixture, as the reviewer noted.
