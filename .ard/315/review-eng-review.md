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
    line: 117
    severity: info
    message: |
      ScopedToken hides its value from String, GoString and slog, but not from json.Marshal.
      Nothing marshals it today. Add a MarshalJSON that redacts it if a later change might.
---
The change follows plan.md (design revision 3): scoped read-token mint and revoke, plus the `internal/jobpod` contract package. It does what the plan lists, and the plan's test cases are written. No blocking findings.

## Checks
- Lint: `make lint` fails only on `internal/roles/testdata/docs/skips/src/_x.go`, an intentionally malformed fixture the change did not touch. `go vet` reported nothing on the changed packages.
- Tests and coverage: `go test -race -cover ./internal/github ./internal/jobpod` passes (88.5% and 92.3%). The uncovered added lines are error branches (`rand.Read`, `json.Marshal`) and `AgentPaths`. The mint, revoke, PAT refusal, token-redaction, manifest cap, refusal and key tests all run.
- Not run: `make docs-check` (mkdocs is probably not installed) and the full `go test ./...`.
