---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/budget/tokens.go"
    line: 52
    severity: low
    blocking: false
    message: |
      tokenFigures calls RoleTokens, which reads the usage of every role for the day, once per candidate on the claim path, although only one role's figure is used. Plan.md accepted two queries per candidate, so this is not a bug. If the claim pass gets slow, cache the per-day map for one scheduling pass.
---
The token cap follows plan.md. The rule `U >= C || C-U < min(W,C)` is implemented as planned, the gate fails closed on read errors, scanners with `ScanPath` stay exempt, and notices are deduplicated per day. The tests cover the rule table, both-cap combinations, midnight reset and notice marker/restart behaviour.

## Checks
- Lint: `make lint` runs `go vet ./...`. It reports only `internal/roles/testdata/docs/skips/src/_x.go:3:8: expected ')', found 'EOF'`. That file is a deliberately broken fixture the change did not touch. Nothing was reported on the changed files.
- Tests and coverage: `go test -cover ./internal/budget/ ./internal/servecmd/` passes (budget 87.1%, servecmd 80.3%). I did not compute per-line diff coverage, but the new functions all have named tests.
