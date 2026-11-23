---
role: engineer
status: done
attempt: 1
---
The reviewer loop now marks a worker's draft PR ready for review once every requested review is done at the PR's current head, so humans no longer click "Ready for review" by hand. The proposal and Gate 2 runbook are updated to match.

## Changes
- `internal/reviewer/ready.go` (new): `maybeReady` checks the rule (open draft App PR on a `worker/` branch against `main`, every requested review `:done`, no `:changes`, `:running` or blocking labels, reviewed head equals the current head or only `.ard/<issue>/` changed since). It calls `MarkReady` through the effects layer and logs `review.marked_ready`, or `review.would_mark_ready` in dry-run. A failure is logged as `review.mark_ready_failed` and retried next tick. `sweepReady` re-checks open PRs that carry a `:done` review label on every tick.
- `internal/reviewer/loop.go`: `Effects` gains `MarkReady`, `Store` gains `Events`. The tick runs the sweep right after the identity lookup, before the credential check, so it works without a model credential and in dry-run.
- `internal/reviewer/run.go`: calls `maybeReady` after close-out. It cannot fail close-out.
- `internal/reviewer/ready_test.go` (new): tests for the cases in the issue.
- `docs/proposal.md`, `docs/runbooks/gate-2.md`, `internal/reviewer/doc.go`, and a comment in `internal/worker/run.go`: wording updated.

## Testing
`make test` and `make lint` pass. New tests cover: marked once when the last review is done; a blocked table (changes, running, unfinished, failed, needs-human, hold, tests-failed, no request, closed, human author, wrong base or branch, already ready); head moved by code, `.ard/` only, git error and no claim record; dry-run writes the ledger only; the sweep catches a missed mark without a credential; a `MarkReady` failure does not fail the tick and is retried.

## Open issues
- The reviewed head comes from the newest claim event per role, and the check fails closed when none exists.
- The fake GitHub client does not support `author:` or `draft:` search qualifiers, so the sweep searches `is:pr is:open label:"<role>:done"` per reviewer role and filters in code.
- There is no dedicated test for the post-close-out call in `run` (the fake finisher does not change labels); the sweep tests cover the same code path. The planned servecmd e2e case was not added.
