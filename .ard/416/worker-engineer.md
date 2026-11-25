---
role: engineer
status: done
attempt: 1
---
Adds an approve key (`a`) to the `ard serve` terminal UI. It runs the operator's own `gh issue edit` through a new effect-layer method (recorded, not run, under dry-run), so the App never adds the approval label, and wakes the triage loop on success.

## Changes
- `internal/effects`: new `Effects.ApproveIssue` (not on `github.Writer`). Live validates the repo and number and runs `gh issue edit N --add-label ard:approved -R repo` with no shell; a missing gh or a not-logged-in error returns `ErrGHUnavailable`. DryRun records it in the ledger.
- `internal/triage`: new `ApprovalQueue` (mutex-guarded) fed by the approval gate and cleared on approval, close or needs-human.
- `internal/snapshot`: `QueueItem`, `Snapshot.Queue`, `Sources.Approvals`.
- `internal/ui`: `a` on a queued selected row opens a confirm screen (number, title, worker, plan link, all sanitised; default No; only `y` approves); result line in the footer; help text.
- `internal/servecmd`: `approveFn` calls the effect, wakes triage, or on `ErrGHUnavailable` shows and opens the issue URL; wiring of queue and approve into the UI.
- `docs/proposal.md`: one sentence in the approval paragraph.

## Testing
`make lint test` passes. Added tests: fake gh on PATH (exact argv, no gh, not logged in, bad input, App client untouched, dry-run ledger), UI (confirm golden, y runs once, Esc/Enter/n/other cancel, non-queue and empty ignored, error line), servecmd approve (wake, dry-run, URL fallback, error), triage queue add/dedupe/remove, snapshot queue. The three existing UI goldens changed for the new footer text.

## Open issues
- The queue is in memory and filled only by the live gate; under `--dry-run` the gate returns before queueing, so no rows are offered there. A closed issue that leaves the triage candidate set stays listed until restart.
- The `a` key only acts on rows shown in the Issues list (issues with activity today) that are also in the queue.
- Approval is still enforced by the existing gate (actor permission, after triage done).
