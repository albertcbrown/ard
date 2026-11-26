---
role: engineer
status: done
attempt: 1
---
The generic-assignment secret rule now skips values that are upper-case snake-case environment variable names, so lines assigning such a name no longer trip the scan or the diff guard. The log scan, committer scan and diff guard share the rule, so all three change together.

## Changes
- internal/redact/rules.go: new `envName` check (at least one underscore, upper-case letters and digits only, at most 64 bytes), called from `placeholder()`; comment on the rule updated.
- internal/redact/scan_test.go: three env-name cases added to the false-positive test; new `TestScannerGenericAssignmentEnvNameBoundaries` table (length cap, no underscore, mixed case, lower case, dot/plus/slash/equals, leading underscore or digit, double underscore all still reported). Fixtures are built at run time.

## Testing
`go test ./internal/...` passes, including `internal/redact`; `go vet` and `gofmt` clean on the package. The full `go test ./...` hit my 2-minute tool timeout on one run, so I ran `./internal/...` instead. Existing true-positive tests are unchanged.

## Open issues
- No docs change: the proposal has no paragraph on generic-assignment.
