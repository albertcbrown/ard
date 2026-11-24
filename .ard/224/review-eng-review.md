---
role: eng-review
status: done
attempt: 2
verdict: approve
---
Both findings from the previous round are fixed. The change follows plan.md: the read-only history mount, the `history` marker, and a fail-closed scanner that uses the same fingerprint salt as the HEAD scanner. The changed packages' tests pass.

## Checks
- Lint: `make lint` reported only `internal/roles/testdata/docs/skips/src/_x.go`, a deliberately broken fixture the change did not touch. Nothing on changed files.
- Tests: `go test` on executor/..., roles, config, scanner and initcmd all passed.
- Diff coverage: not measured. The new Go code is covered by `TestPrepareHistory`, the `TestRunArgv` history case, `TestValidate`, `TestReadsHistory` and the engine test. The shell scanner is covered by `trufflehog_history_test.go`.
- Docker: not run here. `TestDockerHistory` skips without Docker.

## Previous findings
- internal/executor/local/local_test.go (Docker integration test missing): resolved. `TestDockerHistory` in `docker_integration_test.go` checks `ARD_HISTORY`, `git log --all` showing more than one commit, and that a write to `/history` fails.
- internal/executor/local/local.go (host mode does not enforce read-only): resolved. The comments on `Job.History`, `Paths.History` and `Prepare` now say that only the Docker mount enforces it.
