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
      The redaction test checks only that `w.environ()` includes the token. plan.md asked for a test that a secret from a fake executor is masked in a close-out log.
      `closer()`, `workerLoop` and `reviewerLoop` each use `w.environ`, and no test fails if one goes back to `os.Environ`. Add a test that runs the closer's `NewRedactor` on text containing the token.
  - file: "internal/agentjob/admit_test.go"
    line: 90
    severity: medium
    blocking: false
    message: |
      Only `agentjob.ExecJob` is tested for `RoleSource`. `worker.execJob` (internal/worker/run.go:323) and the scanner engine (internal/scanner/engine_exec.go:124) have no assertion.
      Kube `Prepare` rejects a job without `RoleSource`, so a regression there breaks every k8s job. Add an assertion in each.
  - file: "internal/servecmd/k8s_test.go"
    line: 666
    severity: low
    blocking: false
    message: |
      `TestSweepOrphans` covers only k8s; a local executor skipping the sweep is untested. `TestExecutorDrift` tests `checkExecutor` directly, not the paused status the Config callback produces.
---
The change follows plan.md: executor selection, drift refusal, PAT refusal, sweep, new causes and redaction wiring are in place and tested at their core. The round-1 findings are all still present, but none is blocking, so I approve.

## Checks
- `go build ./...` succeeds.
- `go test ./internal/servecmd ./internal/reaper ./internal/cli ./internal/config` passes.
- Lint and coverage were not re-run this round. In round 1 gofmt was clean on the changed files, and `go vet` stopped only on a fixture the change does not touch.

## Previous findings
- internal/servecmd/k8s_test.go (redaction test): still present
- internal/agentjob/admit_test.go (`RoleSource` in worker and scanner): still present
- internal/servecmd/k8s_test.go (local sweep skip, paused status): still present
- internal/scanner/engine_exec.go (info, no scanner cause): no longer applicable
