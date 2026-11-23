---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/worker/run.go"
    line: 693
    severity: high
    blocking: true
    message: |
      Several worker-level test plan cases from plan.md were not written (the worker's report admits it).
      Missing: lead-deferred requests bypassing `when:` (with no duplicates), the store-error fallback to
      labels, the monotonic two-run case (run 2 adds a reviewer once `go.mod` changes, eng-review not duplicated),
      the `worker.no_reviewers` log line, the dry-run zero-SHA fail-open without a warning, and `Matches` not
      being consulted when tests failed. The bypass and monotonic behaviour are the review-gate guarantees,
      so a regression there would silently weaken review. Add these to internal/worker/loop_test.go.
  - file: "internal/config/types.go"
    line: 276
    severity: low
    message: |
      matchWhen swallows a doublestar error (`err == nil && m`). Load validates globs, so this is
      unreachable from config, but a Matches caller with a bad pattern silently never matches (fails closed).
---
The selection logic matches plan.md and the checks pass. It needs changes because the plan's worker-level tests for the deferred bypass, the monotonic rerun, the store-error fallback and the empty-selection log are missing.

## Checks
- Lint: `make lint` (go vet ./...) is clean.
- Diff coverage: `go test -cover` on internal/config (89.1%), internal/worker (83.6%) and internal/closeout (90.4%) passes. I did not produce a per-line profile, so the untested paths above come from reading the tests and the worker's own report.
