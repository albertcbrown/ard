---
role: engineer
status: done
attempt: 1
---
Adds a `needs-network` worker status with a strictly validated `network:` list. Close-out turns it into a new `OutcomeNeedsNetwork` that, for now, ends like `needs-human` with the list in the comment. Nothing is pushed and no PR is opened.

## Changes
- `internal/closeout/rolefile.go`: status `needs-network`, the `NetworkNeed` type and `network:` parsing (1–10 entries, https URL of at most 300 bytes without userinfo, one-line text of at most 200 runes). The list is rejected with any other status, and the status is rejected off the worker stage.
- `plan.go`: `OutcomeNeedsNetwork` and `Plan.Network`. `Build` matches it before the generic not-done case, without reading the worker result, so the Reaper close-out gets the same plan.
- `prepare.go`: takes the list from the redacted re-parse. If that no longer parses, the job ends `needs-human` ("network list unreadable after redaction").
- `comment.go`: header reads `needs-human`; renders the list with each URL in a code span.
- `costs/rows.go`, `reaper/reaper.go`, `reaper/finish.go`: handle the new outcome like needs-human.
- `docs/proposal.md`: role file example, a "Network needs" paragraph and worker loop step 9.
- Tests: `TestParseNetwork`, `TestCheckNeedsNetworkStage`, `TestBuildNeedsNetwork`, `TestPrepareNeedsNetwork`, `TestCommentNeedsNetwork`.

## Testing
`go test ./internal/closeout/... ./internal/costs/... ./internal/reaper/...` passes. `go vet` on those packages is clean and gofmt reports nothing.

## Open issues
- I did not run `make lint test` or the whole repository's tests.
- `make docs-check` could not run because mkdocs is not installed here.
- I added no Run-level test in `closer_test.go`. The no-PR and no-push behaviour rests on `worker/run.go` already handling any non-done status that way, which I read but did not exercise in a test. The outcome falls into `Store.Complete`'s default branch, which I left unchanged.
