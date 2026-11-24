---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/closeout/rolefile.go"
    line: 8
    severity: medium
    blocking: true
    message: |
      `make lint` fails: gofmt flags this file because the "net/url" import is placed after "os".
      Run gofmt -w on the file (imports must be sorted: "net/url" before "os").
  - file: "internal/closeout/rolefile.go"
    line: 346
    severity: medium
    blocking: true
    message: |
      The landing `path` is validated only by checkFindingPath, which allows backticks, newlines and other control characters. Body() in internal/worker/pr.go renders it inside a markdown code span, so a path such as "a`b" (or one with a newline, from a double-quoted YAML string) breaks out of the span and injects markdown into the PR body. URLs are protected against this by checkHTTPS, but the path is not, and setFetched's redaction and scan do not catch it. Reject control characters, backticks, `<` and `>` in the path (or render it safely), and add table cases for them to TestParseFetched.
  - file: "docs/proposal.md"
    severity: low
    message: |
      The issue mentions a CAPTURE.md beside each fixture set (the #47 precedent). It is not in this change, and internal/roles/testdata/lts/api/CAPTURE.md does not exist. If that is meant for a later part, say so; the worker prompt does not mention `fetched:` either.
---
The parsing, PR body and proposal changes follow the plan and are well tested, but `make lint` fails on gofmt and the landing path is not protected against markdown injection in the PR body.

## Checks
- Lint: `make lint` equivalent. `gofmt -l` reports internal/closeout/rolefile.go (import order). `go vet` was not run separately.
- Tests: `go test -cover ./internal/closeout ./internal/worker` pass (closeout 89.6%, worker 82.6%).
- Diff coverage: not profiled line by line. The new parsing and Body code have table tests. setFetched (the redaction and scan path in internal/worker/run.go) has no test of its own.
