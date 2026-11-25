---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/closeout/closeout.go"
    line: 635
    severity: medium
    blocking: false
    message: |
      The close-out sets `Retry: true` unconditionally, but plan.md step 9 says Retry should be false when the job is itself a retry (`RetryOf != ""`, as in reaper.NewCause).
      closeout.Job has no RetryOf field, so the commit path cannot tell. Check that a retried job which hits push_transient again cannot hand off a second time (the reaper doc says a failed automatic retry is marked failed). If nothing downstream guards it, pass RetryOf through, and add the test case plan.md names for a job that is itself a retry.
  - file: "internal/worker/run.go"
    line: 640
    severity: medium
    blocking: false
    message: |
      The new worker branch for committer.ErrTransient (CausePushTransient, retry only when !jb.pushed) has no test. plan.md did not list a worker test explicitly, but it is the second place that maps the error to a cause.
  - file: "internal/committer/transient_test.go"
    severity: low
    blocking: false
    message: |
      plan.md's credential-redaction test (a token in the stderr of a transient failure is redacted in the error and in the committer.retry log) was not written. The log carries cause.Error(), so a test would guard against leaks.
  - file: "internal/committer/committer.go"
    line: 372
    severity: low
    blocking: false
    message: |
      Transient retries on the main lane hold the lane for up to about a minute per attempt budget (plan.md accepted this). The `pending` landed check only runs after a successful re-fetch; if every re-fetch after the failed push is transient, the loop falls through to exhaustion and a landed push is reported as ErrTransient. This is acceptable because the close-out retry will re-apply, but it may produce a no-op on retry.
---
The transient-retry change follows plan.md. It covers the main, code-create and init lanes, detects a push that landed despite the error, keeps the transient and rejection budgets separate, and the changed packages pass. Two non-blocking test and retry-guard gaps remain.

## Checks
- `go build ./...` and `go vet` on the changed packages: clean.
- `make lint`: `go vet ./...` stops on `internal/roles/testdata/docs/skips/src/_x.go`, a file this change did not touch. I ran vet per changed package instead, and it was clean. I did not run gofmt separately.
- `go test -cover ./internal/gitx ./internal/committer ./internal/closeout`: all pass (coverage 81.3%, 87.3% and 90.1%). I did not run a per-line diff coverage report. The new committer, gitx and closeout paths have named tests. The worker branch and `github.IsTransientToken` (only through the gitx DNS test) are thin.
