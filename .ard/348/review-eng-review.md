---
role: eng-review
status: done
attempt: 3
verdict: approve
---
The change implements what the issue asks. `ARD_BOT_LOGIN` is derived from the App identity, and a different existing value is replaced only after a confirmation. `SetRepoVariable` sits on the effect layer, with the operator token held in memory only. The embedded workflow is checked and `--print-workflow` is added. The tests cover the main paths and the failure paths. Approved.

## Checks
- `go test ./internal/setupcmd ./internal/effects ./internal/cli ./internal/github` passes.
- `go vet` on setupcmd and effects is clean. `gofmt -l` flags only `internal/roles/testdata/.../_x.go`, a test fixture the change did not touch.
- `make lint` and a coverage profile were not run. The tests exercise the added code paths.

## Previous findings
- internal/setupcmd/botlogin_test.go (TestBotLoginNeverFromConfig missing): resolved, the test is present.
- internal/setupcmd/botlogin.go (unquoted `--body` value): resolved, `setCmd` quotes the value and the test expects the quoted form.
