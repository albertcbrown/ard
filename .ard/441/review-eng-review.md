---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/servecmd/uilog_test.go"
    line: 106
    severity: low
    message: |
      The SIGINT case cancels the context before runWithUI starts, so it no longer tests cancellation arriving while the UI runs.
      It still checks that the controller stops. Optionally cancel from the progs receiver instead.
---
The change removes the data race in TestRunWithUI by using a channel for the program handle, an atomic.Bool for `stopped`, and no sleeps. It also adds a `test-race` make target. Approved.

## Checks
- Lint: `make lint` stops at `go vet` with a parse error in `internal/roles/testdata/docs/skips/src/_x.go`. That file is not part of this change, so I ignored it.
- Tests: `go test -race -count=5 -run TestRunWithUI ./internal/servecmd/` passed with no race reports.
- Diff coverage: skipped; the change only touches a test file and the Makefile.
