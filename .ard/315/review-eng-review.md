---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/jobpod/contract.go"
    line: 56
    severity: low
    message: |
      AgentPaths has no test. It is a plain constant mapping, so this does not block.
      A one-line test comparing it with the Mount* and Job* constants would catch drift later.
  - file: "internal/github/scoped.go"
    line: 25
    severity: info
    message: |
      ScopedToken hides its value from String, GoString and slog, but not from json.Marshal.
      Nothing marshals it today. Add a MarshalJSON that redacts it if a later change might.
---
The change follows plan.md (design revision 3): scoped read-token mint and revoke, plus the `internal/jobpod` contract package. The plan's test cases are written and no finding blocks, so this is approved.

## Checks
- Lint: not re-run this round. The previous round's `make lint` failed only on an untouched, intentionally malformed fixture.
- Tests and coverage: `go test -race -cover ./internal/github ./internal/jobpod` passes (88.5% and 92.3%). The uncovered added lines are error branches and `AgentPaths`.
- Not run: `make docs-check` (mkdocs is probably not installed) and the full `go test ./...`.

## Previous findings
- internal/jobpod/contract.go (AgentPaths untested): still present, low, non-blocking.
- internal/github/scoped.go (ScopedToken not redacted in json.Marshal): still present, info. It is reported at line 25, where the type is declared.
