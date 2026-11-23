---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/servecmd/triage.go"
    line: 47
    severity: low
    blocking: false
    message: |
      The production `planExists` (fetch main into a private ref, then FileAt) has no test; the triage tests only use a fake.
      It mirrors the janitor's fetch pattern, so the risk is small. Consider a test against a local bare repo if gitx has such a helper.
---
The gate now refuses with one comment, `ard:needs-human` and a `no-plan` gate event when `plan.md` is missing, and the existing path is unchanged. Tests cover approval on and off, a failing check, and a waiting role. The docs are updated as the issue asked.

## Checks
- Lint: `make lint` fails in `go vet ./...` on `internal/roles/testdata/docs/skips/src/_x.go` (a deliberately broken testdata file the change did not touch). Nothing was reported on the changed files; `go vet` on the changed packages and the gofmt check are clean.
- Tests: `go test ./internal/triage ./internal/servecmd` passes.
- Diff coverage: not run as a profile. The new gate branches are exercised by `TestGateNoPlan`; only the servecmd wiring is not (see finding).
