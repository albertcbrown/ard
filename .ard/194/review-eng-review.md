---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/reviewer/run.go"
    line: 504
    severity: medium
    message: |
      The post-close-out maybeReady call in run has no test. plan.md listed TestMarkReadyFailureDoesNotFailCloseout and a servecmd e2e case; neither exists (the worker's report says so).
      The sweep tests exercise maybeReady itself, so the logic is covered, but the wiring (self from c.pr.Author, call after finish) is not. Add a harness test that runs a review to close-out and asserts MarkReady. Not blocking because the per-tick sweep would catch a miss.
  - file: "internal/reviewer/ready.go"
    line: 120
    severity: low
    message: |
      reviewedHeads loads every claim event in the store, with no filter or limit, once per candidate PR per tick. This grows with history. Filter by issue/role in the query, or fetch once per sweep, if EventFilter allows it.
---
The change matches plan.md and the issue: the rule is checked after close-out and on every tick, it fails closed on an unknown head, and the docs are updated. The only gaps are an untested post-close-out wiring and an unbounded event query, neither blocking.

## Checks
- Lint: `go vet ./internal/reviewer/` is clean; `make lint` was not run.
- Diff coverage: `go test -cover ./internal/reviewer/` passes at 81.8%. The new `ready.go` paths are covered through the sweep tests; the call added to `run.go` is not executed by a dedicated test.
