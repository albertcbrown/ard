---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/closeout/closer_test.go"
    severity: high
    blocking: true
    message: |
      plan.md's test plan names a Run-level test in closer_test.go that was not written.
      It should show that a needs-network close-out posts exactly one comment, adds `ard:needs-human`, removes `:running`, requests nothing, opens no PR and pushes nothing. The issue's acceptance criteria say "nothing pushed, no PR, one comment", and this is the core of the behaviour. Today that rests on `worker/run.go` and `Store.Complete`'s default branch, and the worker's report admits it did not exercise them. Add the test with the existing Run helpers.
  - file: "internal/closeout/closeout.go"
    line: 24
    severity: low
    message: |
      The edited comment line is now far over the wrapped width of the neighbouring lines. Re-wrap the paragraph.
---
The parser, `Build`, the redaction re-parse and the comment rendering match plan.md and the issue, and are well tested. The Run-level test that plan.md lists for the no-PR, no-push behaviour is missing, so this needs changes.

## Checks
- Lint: `make lint` failed in this environment on a pre-existing testdata fixture (`internal/roles/testdata/docs/skips/src/_x.go`, a parse error in a file the change did not touch). It reported nothing on the changed files.
- Diff coverage: `go test -coverprofile ./internal/closeout/` passed with 90.1% coverage. `RenderComment` is at 100%, `Build` at 97.2%, `networkOf` at 92.3% and `networkTypes` at 80%. The gaps are small error branches.
