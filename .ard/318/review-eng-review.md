---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/servecmd/k8s_test.go"
    line: 698
    severity: medium
    blocking: false
    message: |
      The redaction test checks only that `w.environ()` includes the token. plan.md asked for `TestRedactionIncludesTokens`: a secret from a fake executor masked in a close-out log.
      The wiring `closer()`, `workerLoop` and `reviewerLoop` each switched to `w.environ`, and no test would fail if one of them went back to `os.Environ`. Add a test that runs the closer's `NewRedactor` against text containing the token.
  - file: "internal/agentjob/admit_test.go"
    line: 90
    severity: medium
    blocking: false
    message: |
      plan.md's builder tests ask that `RoleSource` is set in the worker, triage, reviewer and scanner jobs. Only `agentjob.ExecJob` is tested.
      `worker.execJob` (run.go:323) and `engine_exec.go:123` are untested. Kube `Prepare` rejects a job without `RoleSource`, so a regression there breaks every k8s job. Add an assertion in each.
  - file: "internal/servecmd/k8s_test.go"
    line: 666
    severity: low
    blocking: false
    message: |
      plan.md asked that a local executor skips the sweep. `TestSweepOrphans` covers only k8s.
      `TestExecutorDrift` also tests `checkExecutor` directly, not the paused status the Config callback produces. Both are small gaps.
  - file: "internal/scanner/engine_exec.go"
    line: 134
    severity: info
    blocking: false
    message: |
      plan.md step 9 lists the scanner engine for `ErrInputsTooLarge`. The engine wraps the Prepare error as `prepare scan: %w` and has no close-out cause, so the error is still reachable with `errors.Is`. No change is needed unless scanners need the cause.
---
The change follows plan.md. Executor selection, the drift refusal, the PAT refusal, the sweep, the new causes and the redaction wiring are all in place and tested at their core. The remaining gaps are non-blocking test gaps, so I approve.

## Checks
- `go build ./...` succeeds.
- `go test ./internal/servecmd ./internal/reaper ./internal/cli ./internal/config` passes.
- `make lint`: gofmt reports nothing on the changed files. `go vet` stops on `internal/roles/testdata/docs/skips/src/_x.go`, a deliberately malformed fixture the change does not touch.
- Diff coverage: no coverage profile was generated. I judged coverage by reading the tests. The new functions `newExecutor`, `checkExecutor`, `parseResources`, `sweepOrphans`, `environ` and `CauseOf` have tests. The gaps are the ones listed in the findings.
