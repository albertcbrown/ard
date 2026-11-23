---
role: eng-review
status: done
attempt: 1
verdict: approve
findings:
  - file: "internal/servecmd/triage.go"
    line: 48
    severity: low
    blocking: false
    message: |
      The production `planExists` (fetch main into a private ref, then FileAt) still has no test; the triage tests only use a fake.
      plan.md suggested a temp-repo test if a cheap fixture exists. The risk is small since it mirrors the existing fetch pattern, but a test with and without `.ard/<n>/plan.md` on main would cover the wiring.
---
The gate refuses with one comment, `ard:needs-human` and a `no-plan` gate event when `plan.md` is missing, and the existing path is unchanged. Tests cover approval on and off, a failing check and a waiting role, and the docs state the check. Approved.

## Checks
- Tests: `go test ./internal/triage ./internal/servecmd` passes.
- Lint: not re-run this round. The previous round found `make lint` failing in `go vet` on a testdata file the change does not touch, and nothing on the changed files.
- Diff coverage: not run as a profile. `TestGateNoPlan` exercises the new gate branches; only the servecmd wiring is untested (see finding).

## Previous findings
- internal/servecmd/triage.go (planExists has no test): still present, low and non-blocking.
