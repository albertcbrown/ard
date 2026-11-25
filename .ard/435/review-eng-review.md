---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/worker/prretry.go"
    line: 114
    severity: low
    blocking: false
    message: |
      reopenPR does not check ctx.Err() after createWithRetry fails, unlike openPR in run.go.
      On shutdown during backoff the job is failed with pr_create_failed and the record is
      rewritten, instead of being left running for the Reaper. Add the same ctx.Err() guard
      before failCreate.
---
The change follows plan.md and the issue. CreatePR is retried with backoff, a failed create after a push records the close-out and ends with `pr_create_failed`, and removing the label reopens the PR without an agent when the branch is unchanged. The new paths have tests, and only a small shutdown edge case remains.

## Checks
- Lint: `make lint` printed dependency downloads and one parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file this change does not touch. I treat that as pre-existing and found no lint problems in the changed files.
- Tests with coverage: `go test -cover ./internal/worker ./internal/state ./internal/closeout` passes (81.7%, 84.0%, 90.2%). The new tests cover retry then open, always failing, reopen, branch moved, existing PR, dry-run and no-push.
