---
role: eng-review
status: done
attempt: 2
verdict: approve
findings:
  - file: "internal/initcmd/initcmd.go"
    line: 62
    severity: low
    blocking: false
    message: |
      No test drives `Init`/`run` with a token-kind `deps` to check that `AuthKind` reaches `initBody` in the PR body.
      `resolveAuth` and `initBody` are each tested, but the wiring between them is not. Consider one `run` case with a token-kind `deps` that asserts the PR body contains "token's user".
---
The change drops the token refusal in `ard init --pr`, reuses serve's `GhFallback`, words the PR body for both auth kinds, and updates the docs as the issue asks. It is approved. The only remaining finding is the non-blocking wiring test gap from the previous round.

## Checks
- Lint: not re-run. The previous round found `make lint` stopping on an untouched fixture, which this round did not change.
- Tests: `go test ./internal/initcmd -cover` passes with 82.3% coverage. `resolveAuth` and the new `initBody` branches are executed.

## Previous findings
- internal/initcmd/initcmd.go (wiring test for AuthKind into the PR body): still present, low and non-blocking.
