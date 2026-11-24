---
role: eng-review
status: done
attempt: 2
verdict: approve
findings:
  - file: "internal/worker/run.go"
    line: 496
    severity: medium
    message: |
      setFetched (redaction, secret scan, drop-on-hit) has no test of its own. Only Body rendering and parsing are tested.
      Add a unit test where a fetched field is redacted or the scanner reports a hit, and assert jb.fetched is nil.
  - file: ".ard/workers/engineer/prompt.txt"
    severity: low
    message: |
      The worker prompt does not tell the worker to record fetched inputs under `fetched:`, and no CAPTURE.md was added. The proposal now says the worker does both. Update the prompt in a follow-up, or say in the proposal that this is a later part.
---
The change parses and validates `fetched:`, renders it in the PR body and updates the docs as planned. Both blocking findings from round 1 are fixed, so this is approved.

## Checks
- Lint: no `make lint` was run. `gofmt -l internal/` is clean on the changed files. The only output is a parse error in a testdata fixture the change did not touch. `go vet ./internal/closeout ./internal/worker` printed only dependency downloads and no problems.
- Tests: `go test -cover` on closeout (89.7%) and worker (82.6%) pass.
- Diff coverage: not profiled line by line. Parsing and Body are covered by table tests. setFetched is not covered.

## Previous findings
- internal/closeout/rolefile.go (import order): resolved.
- internal/closeout/rolefile.go (path markdown injection): resolved. The path now rejects control characters, backticks and angle brackets, with test cases.
- docs/proposal.md (CAPTURE.md and prompt): still present, now reported as a low finding on the prompt.
