---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change matches plan.md: `runArgv` adds `--network none` for `NetworkNone`, the worker marks test steps `NetworkNone` and its other steps `NetworkAdvisory` when `network: false`, and both are tested. Approved.

## Checks
- Lint: `make lint` printed only dependency download lines and one parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a testdata file the change does not touch. `gofmt -l` is clean on the changed packages.
- Tests: `go test ./internal/executor/local/ ./internal/worker/ -run 'TestRunArgv|TestNetworkPerStep'` passes.
- Diff coverage: not measured. The added lines in `docker.go` and `run.go` are exercised by the new table cases and `TestNetworkPerStep`, which covers a t1, fix, t2 loop.
