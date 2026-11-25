---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/servecmd/uilog.go"
    line: 61
    severity: low
    blocking: false
    message: |
      When the rotation rename fails, Write has already closed c.f but leaves it set, so every later write goes to a closed file and returns an error, and logging stops for good.
      Reopen the file (or set c.f = nil) on the rename-failure path so the writer recovers or fails cleanly. The rotation failure path has no test.
  - file: "internal/servecmd/serve.go"
    line: 294
    severity: low
    blocking: false
    message: |
      The UI wiring in serveLoops (serve.log redirect, the `logs:` line on exit, the call to runWithUI) is exercised by no test. Only uiWanted, openServeLog and runWithUI are tested on their own. Consider one test with a stubbed uiWanted that checks log output goes to the file.
---
The change follows the plan: TTY detection, `--ui`/`--no-ui`, a capped 0600 serve.log, a sanitised read-only UI and a graceful shutdown path. I found no blocking problems, so this is approved.

## Checks
- Lint: `make lint` printed only a parse error in `internal/roles/testdata/docs/skips/src/_x.go`, a file the change did not touch. It reported nothing on the changed files.
- Coverage: `go test -cover ./internal/servecmd ./internal/ui ./internal/cli` passes (78.4%, 85.3% and 95.8%). The uncovered added code is `OpenBrowser`, `View`, and the rotation and error branches of `uilog.go`.

## Previous findings
- internal/ui/keys.go (OpenBrowser output): no longer applicable. `exec.Command` with nil Stdout and Stderr connects them to the null device, so the opener cannot write onto the screen.
