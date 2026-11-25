---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change matches the issue and plan: `runArgv` adds `--network none` only for `NetworkNone`, the worker test step is `NetworkNone` when `network: false`, and other steps are `NetworkAdvisory`. Both behaviours are tested. Approved.

## Checks
- Tests: `go test ./internal/executor/local/ ./internal/worker/ -run 'TestRunArgv|TestNetworkPerStep'` passes.
- Lint: not re-run this round. The previous round's `make lint` found nothing on the changed lines, and the diff is unchanged in substance.
- Diff coverage: not measured. The added lines are executed by the new table cases and `TestNetworkPerStep`.

## Previous findings
- The previous review had no findings (approve), so there is nothing to carry over.
