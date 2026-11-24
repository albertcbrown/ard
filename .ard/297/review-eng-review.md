---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/executor/local/hostwarn.go"
    line: 68
    severity: medium
    blocking: false
    message: |
      The "host mode logs the not-enforced line" criterion has no test.
      Nothing asserts that warnHostNetwork fires from Start, either on the untrusted path (warnHostJob) or the trusted-host else-branch in proc_unix.go. Likewise nothing tests that Prepare copies Job.Network into the Handle, that ExecJob and the worker and scanner call sites set NetworkAdvisory, or that Start itself (not just checkNetwork) rejects NetworkNone with a credential. Add a hostwarn_test case and a Prepare/Start test.
  - file: "internal/executor/local/hostwarn.go"
    line: 70
    severity: low
    blocking: false
    message: |
      The slog call passes "msg" as an attribute key, which collides with the record's built-in message key. Text and JSON handlers will emit a duplicate msg field. Use a key such as "note".
  - file: "internal/executor/executor.go"
    line: 29
    severity: low
    blocking: false
    message: |
      The Network type and consts were inserted between the "// Job describes one run." comment and type Job. That comment now sits above Network and Job lost its doc comment. Move the new block above the comment.
---
The change implements the config, executor plumbing, env var, prompt line and warnings as the issue describes, and the targeted tests pass. The gaps are test coverage for the host log line and the Prepare/Start wiring, which do not block.

## Checks
- Lint: no Makefile lint run; `go vet ./internal/...` was clean.
- Tests: `go test` on config, configcmd, executor/..., agent/..., agentjob all pass.
- Diff coverage: not measured. By reading, the host log line, Prepare's copy of Network and the ExecJob/worker/scanner assignments are not exercised by any test.
