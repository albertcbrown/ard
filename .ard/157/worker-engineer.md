---
role: engineer
status: done
attempt: 1
---
A non-lead triage claim on a human-filed issue with an unchanged stored admission now logs a new reason, `admitted`, instead of `app-authored`. `app-authored` is kept for App-authored issues only.

## Changes
- `internal/intake/intake.go`: new `ReasonAdmitted` ("admitted").
- `internal/triage/candidates.go`: `consider` no longer gives every candidate a default reason. It sets `ReasonAppAuthored` in the App-author branch and `ReasonAdmitted` in the stored-admission branch. The gate path still takes the record's reason.
- `internal/triage/loop_test.go`: two `TestCandidates` subtests call `consider` directly. One checks an admitted human issue gives `admitted`. The other checks an App-authored issue gives `app-authored`.

## Testing
Ran `go test ./internal/triage/... ./internal/intake/...`, which passes, and `gofmt -l internal`, which reports nothing. I did not run the full `make test`.

## Open issues
None.
