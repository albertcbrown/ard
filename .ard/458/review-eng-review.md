---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/initcmd/initcmd.go"
    line: 62
    severity: low
    blocking: false
    message: |
      No test drives `Init`/`run` with a token to check that `AuthKind` reaches `initBody` and that a live token run is no longer refused.
      `resolveAuth` and `initBody` are each tested, but the wiring between them is not. Consider one `run` case with a token-kind `deps` that asserts the PR body contains "token's user".
---
The change removes the token refusal in `ard init --pr` and reuses serve's `GhFallback`. It words the PR body for both auth kinds and updates the proposal and runbook as the issue asks. `resolveAuth` has table tests for the App, env token, gh fallback, no gh, not logged in and conflicting credentials cases. Approved.

## Checks
- Lint: `make lint` stops on `internal/roles/testdata/docs/skips/src/_x.go` (a syntax error in a fixture the change did not touch). `go vet ./internal/initcmd` and `go build ./...` are clean.
- Tests: `go test ./internal/initcmd -cover` passes, 82.3% coverage for the package. `resolveAuth` and the new `initBody` branches are executed by the new tests.
