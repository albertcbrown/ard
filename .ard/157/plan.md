# Plan for #157

## Implementation

### Steps
1. `internal/intake/intake.go` (Reason constants, ~line 56): add `ReasonAdmitted Reason = "admitted" // stored admission, body unchanged`. Leave `ReasonAppAuthored` as is. Reason: a distinct, stable value for slog.
2. `internal/triage/candidates.go`, `(*tick).consider`: keep the default `reason: intake.ReasonAppAuthored` only for the App-authored early return (line ~106). In the non-lead branch that returns a matching stored admission (line ~122, `return c, true, nil` after the `stored == nil` check), set `c.reason = intake.ReasonAdmitted` first. The later `c.rec`/`rec.Reason` override stays untouched. Preferably build `c` without a default reason, setting `ReasonAppAuthored` only in the author branch, so no path inherits a wrong default.
3. Test (see below) in `internal/triage/loop_test.go`.

### Test plan
- Command: `go test ./internal/triage/... ./internal/intake/...` (repo-wide: `make test`).
- New subtest in `TestCandidates` (`internal/triage/loop_test.go`): "a non-lead request on an admitted human issue claims with reason admitted". Input: `h.human(...)` issue with the architect request label plus a stored admission via `h.store.SetAdmission` with `BodyHash: agentjob.BodyHash(h.issue(n))` (pattern in `fanin_test.go:394`). Expected: one claim, and the candidate/claim reason is `intake.ReasonAdmitted`, not `app-authored`. Check the reason either by calling `consider` directly or by swapping `quiet` for a buffer-backed slog handler and looking for `reason=admitted` in the `triage.claim` line.
- Regression: an App-authored issue (existing `TestAppAuthoredIssueIsExemptFromTheHash`) still claims; assert `reason=app-authored` there if the log capture is easy.
- Edge: non-lead with no admission stays skipped (existing subtest); changed body hash still goes through the gate and uses the record's reason.

### Risk
Low. Internal log value only. The admission behaviour and stored records are unchanged. No protected paths from merge.protected_paths are touched. No docs or tooling in the repo list reason values (searched docs and README).

### Size
S. Files: `internal/intake/intake.go`, `internal/triage/candidates.go`, `internal/triage/loop_test.go`.
