---
role: engineer
status: done
attempt: 1
---
A GH006 protected-branch rejection on a main-lane push is now retried only when main moved since the lease (a lost race); otherwise it fails at once with a distinct protection cause, so a real protection failure still surfaces.

## Changes
- gitx: new `ErrProtected`; push stderr mapping extracted into the pure `classifyPush` (stale, non-fast-forward, protected, transient, in that order). Re-exported in effects.
- committer: new `ErrProtected`. `lane.protectedRace` re-fetches main on GH006. A tip different from the lease is a lost race: logged as `committer.retry` with `reason=protected_race`, old and new SHAs, and counted in the `maxPushes` rejection budget. An unchanged tip fails with an error naming the ruleset bypass list and `ard setup --check`. A branch lane fails at once. `committer.failed` carries `reason=protected`. Transient budgets stay separate.
- closeout: new `CauseProtected` (`push_protected`, not transient, no retry) and a failed plan for it.
- worker: names the push failure kind "protected".
- docs/proposal.md: the committer paragraph states the rule.

## Testing
`go test` on gitx, committer, closeout, worker and effects passes; `go vet` runs clean. `make lint` only prints a parse error from a deliberately broken testdata file, which this change does not touch. Added a `classifyPush` table test, `internal/committer/protected_test.go` (race retried, not moved, branch lane, race budget, mixed budgets) and a closeout test.

## Open issues
- A maintainer should confirm the rule that GH006 is retried only when main moved.
- A transient failure of the re-fetch after GH006 is returned as is, not retried through the transient path. A non-transient fetch error also fails the close-out.
- The cause string `push_protected` was my choice.
