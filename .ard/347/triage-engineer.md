---
role: engineer
status: done
attempt: 1
---
Adds read-only ruleset, App permission and merge-setting rows to `ard setup`, checked against a policy built into a new `internal/setupcmd/policy.go`. Four new read methods go on `github.Reader` and the fake, with `manual` rows when admin read is denied. Size L, risk medium because the reader interface grows.

## Open issues
- The issue asks for one new reader method, but three more are needed: merge settings, the App's permissions, and the `ARD_VERSIONING` repository variable. A maintainer may want to confirm that.
- Whether the App's token can read ruleset bypass actors and repository variables needs checking with a real token; the plan treats a forbidden response as `manual`.
