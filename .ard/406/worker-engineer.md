---
role: engineer
status: done
attempt: 1
---
Close-out now wakes the loops that have new work (next stage, fan-in gate, merger, review round), and a new approval watch polls once a minute for a human adding ard:approved or removing ard:needs-human / ard:executor:failed and wakes the owning loop. Hand-offs no longer wait for the next poll interval.

## Changes
- config: new `intervals.approvals` (default 1m, minimum 1m) in types, defaults, raw parsing, keys, render and golden file.
- controller: `Controller.WakeLoops(reason, names...)` with debounce (reuses the slot-wake window), coalescing, ignores unknown names and calls after shutdown; logs `controller.wake reason=... loops=[...]`. New `ApprovalWatch` loop (no slot, no agent, read-only; skips when paused).
- closeout: pure `WakeTargets(Plan)` and `Deps.Wake`, called once after a job closes; no wake on failed, retry, stale, needs-human or dry-run.
- servecmd: late-bound controller hook (`wiring.ctl`), `Wake` passed to the Closer, the watch registered as loop `approvals` (also selectable with `--loops`).
- docs: proposal (job slots section, intervals example) and configuration table.

## Testing
`make lint test`: all tests pass (`go vet` prints a parse note for an intentionally broken testdata file, which is not new). Added tests: config (default, minimum, golden), `WakeTargets` per outcome, Closer wakes (lead, review changes, failed effect then resume, dry-run), controller wake (named loops only, burst coalescing, wake during a tick, paused, shutdown), approval watch (hit, dedupe, unblocked, paused, search error), servecmd registration.

## Open issues
- A label removal cannot be searched, so the unblock watch keeps an in-memory set; a removal while ard is down is left to the loops' own intervals.
- The proposal's gate, worker and reviewer loop sections were not edited separately; the hand-off behaviour is described in the "One pool of job slots" section.
- The "PR marked ready → merger" wake is derived from the review `:done` label, since close-out itself does not mark PRs ready. A review round request wakes reviewfix and workers.
- No new triage-gate test was added; the gate is unchanged and a wake only triggers an ordinary tick.
