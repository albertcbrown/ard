---
role: engineer
status: done
attempt: 1
---
Stops the worker from guessing a `chore:` prefix when an issue has no `type:*` label: the PR and commit title is now `<issue title> (#<n>)` in that case. The `type:*` label stays the only source of the prefix.

## Changes
- `internal/worker/type.go`: `CommitType` returns an empty string for no or unrecognised labels.
- `internal/committer/code.go`: `CodeMessage` accepts an empty type and then leaves out the `<type>: ` prefix; the length budget still counts the real prefix.
- `internal/worker/unit_test.go`, `internal/committer/code_test.go`: updated and new cases for empty type, unprefixed title and truncation.
- `docs/proposal.md` (worker section, item 6): records the decision and that the prefix is never taken from the plan or the lead's routing.

## Testing
Ran `go test ./...`; all pass. `gofmt -l internal` reports nothing.

## Open issues
- Unlabelled issues now get unprefixed titles, so a maintainer must apply a `type:*` label at intake to get a prefix.
- Taking the prefix from the lead's routing was rejected: it needs a change to the lead prompt under `.ard/triage/`.
