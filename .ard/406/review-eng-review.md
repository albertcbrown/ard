---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "docs/proposal.md"
    severity: low
    message: |
      The issue asked for updates to the triage gate, worker and reviewer loop sections, but only the "One pool of job slots" paragraph and the intervals example changed.
      The worker's report discloses this. Add a short pointer in those sections so readers of the loop descriptions see that hand-offs wake the next loop and that approvals are polled by the watch.
  - file: "internal/controller/approvals_test.go"
    line: 726
    severity: info
    message: |
      The unblock test accepts two orderings because the wake list comes from ranging over a map. Sorting `wake` in `ApprovalWatch.Tick` (internal/controller/approvals.go:560) would make the order deterministic and the assertion exact. No action needed.
---
The change matches the plan and the issue. Close-out wakes the next loops only after every label effect and the Closed save succeed, and not in dry-run. The approval watch is read-only and skips the poll while paused. Wake coalescing and the pause, shutdown and dry-run paths have tests. Approved, with two non-blocking notes.

## Checks
- Lint: `make lint` reported nothing on the changed files. The only output was the existing parse note for the intentionally broken `internal/roles/testdata` file.
- Diff coverage: `go test -cover` on closeout (90.2%), controller (96.0%), config (89.7%) and servecmd (79.6%) all passed. I did not do a per-line coverage profile. The new functions `WakeTargets`, `Closer.wake`, `WakeLoops`, `flushWakes` and `ApprovalWatch.Tick` each have direct tests.
