---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/gitx/version.go"
    line: 15
    severity: info
    message: |
      MinVersion 2.47.0 is a conservative guess, as the comment and plan.md say: only 2.39.5 (fails) and 2.47.3 (passes) were observed. A maintainer could bisect with intermediate gits and lower it.
---
The change follows plan.md: a shared version check in gitx, called first in `ard serve` and `ard config check`, with the docs sentence and tests for every case in the test plan. Approved.

## Checks
- Build: `go build ./...` succeeds.
- Vet: `go vet` is clean on gitx, configcmd and servecmd.
- Tests: `go test ./internal/gitx ./internal/configcmd ./internal/servecmd` passes. This includes the new parse, compare, check and refuse-old-git tests.
- Diff coverage: not run with a profile. The new functions and both call sites are exercised by the new tests.
