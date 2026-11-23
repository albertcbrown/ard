---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/triage/candidates.go"
    line: 123
    severity: medium
    blocking: true
    message: |
      Removing the default reason leaves the lead's claim on an already-admitted human issue with an empty reason.
      When the lead role has a stored admission with an unchanged body hash, `gate` returns vPass with a nil record (internal/triage/intake.go:142-143). So `c.reason` is never set and the claim logs `reason=` (empty). That is the same class of bad audit log the issue reports. Set `c.reason = intake.ReasonAdmitted` in that case too, for example by defaulting it just before the final return when `rec == nil`. Add a lead-role test with a stored matching admission.
---
The change fixes the non-lead path and adds tests for it and for App-authored issues. However, dropping the default reason leaves the lead's matching-admission path (vPass, nil record) with an empty reason, so this needs changes.

## Checks
- `go test ./internal/triage/... ./internal/intake/...`: pass.
- `go vet` on both packages: clean.
- Diff coverage: not run separately. The new branches (admitted, app-authored) are executed by the added subtests. The lead matching-admission branch has no test.
