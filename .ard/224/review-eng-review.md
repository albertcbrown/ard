---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/executor/local/local_test.go"
    line: 794
    severity: high
    blocking: true
    message: |
      The Docker integration test that plan.md's test plan names was not written (worker-engineer.md admits this).
      Nothing runs `git --git-dir /history log --all` in a container, nothing checks that a write to `/history` fails,
      and nothing checks whether the `safe.directory` setting is needed. The core of the executor change (a read-only
      mount that git can actually read) is only tested as argv strings. Add `docker_integration_test.go`, skipped
      without Docker, as plan.md describes.
  - file: "internal/executor/local/local.go"
    line: 258
    severity: low
    message: |
      In host mode the job gets `RepoDir` directly, so the "read-only" history is writable by the scan process. The
      Docker path is `:ro`. Scanners are trusted code, so this is acceptable, but `Paths.History` and the `Job.History`
      comments say "read-only". Say in the comment that host mode does not enforce it.
---
The executor change and the scanner follow plan.md, and the changed packages' tests pass. The Docker integration test that plan.md names is missing, so the read-only mount is never exercised against a real container; that blocks approval.

## Checks
- Lint: `make lint` ran. It reported only `internal/roles/testdata/docs/skips/src/_x.go` (a deliberately broken fixture the change did not touch). Nothing on changed files.
- Tests: `go test` on executor, executor/local, roles, config and scanner passed.
- Diff coverage: not measured. The new tests exercise the added Go code, `ReadsHistory`, `Prepare`, `runArgv` and the engine. The shell scanner is covered by `trufflehog_history_test.go` with fake binaries.
- Docker was not run here.
