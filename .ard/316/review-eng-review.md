---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/jobpost/post.go"
    line: 125
    severity: medium
    blocking: false
    message: |
      The error from `tarball.Pack` is discarded, so a failure mid-stream leaves a truncated 200 response with nothing logged.
      The headers are already sent, so the status cannot change. The client can only detect the cut through an invalid gzip/tar trailer.
      Abort the connection on error (for example `panic(http.ErrAbortHandler)`), so the controller sees a transport error and retries instead of a short body. Add a test for it.
  - file: "internal/jobpost/post_test.go"
    line: 1226
    severity: low
    blocking: false
    message: |
      plan.md's test plan lists "the log lacks the key and Authorization". TestPost does not test it.
      The server's ErrorLog is discarded, so the property probably holds, but nothing asserts it. A short test that captures the logger output would pin it.
  - file: "internal/jobexec/exec.go"
    line: 152
    severity: low
    blocking: false
    message: |
      The `finish` failure paths (Sync and write errors) and the `128+signal` branch of `exitCode` are never executed by the tests.
      The common paths are covered, so this does not block.
  - file: "internal/jobpre/pre.go"
    line: 243
    severity: low
    blocking: false
    message: |
      `installBinary` error paths and the `History` plus `Sidecar` failure paths are untested (about 60% coverage on this function). Low risk.
---
The change matches plan.md: three hidden commands, `CheckoutTree`, handler wiring and docs. The core paths (pre layout and exit codes, exec deadline, kill and marker, post auth/409/429) are all tested. The only notable gap is a swallowed `Pack` error in job-post, which does not block.

## Checks
- Lint: `make lint` runs `go vet ./...` and `gofmt -l .`. The only complaint was `internal/roles/testdata/docs/skips/src/_x.go`, a testdata file this change did not touch.
- Tests: `go test` on gitx, cli, jobpre, jobexec, jobpost, cmd/ard and docscheck all pass.
- Diff coverage (from `go test -cover`): jobpre 79%, jobexec 84%, jobpost 93%, cli 95%, gitx 81%. Uncovered lines are the error paths listed in the findings. `CheckoutTree` is 60%, only the error returns. Hooks are disabled by the git runner's `core.hooksPath=/dev/null`, and `TestCheckoutTree` asserts that.
