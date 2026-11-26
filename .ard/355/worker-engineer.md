---
role: engineer
status: done
attempt: 1
---
The aws-access-key scan rule no longer reports 20-character ids ending in the documented placeholder suffix, so files holding AWS's example key no longer get an agent log withheld. Every other AKIA/ASIA id is still a finding.

## Changes
- internal/redact/rules.go, scan.go: optional per-match `skip` on a rule; `awsExample` exempts only 20-char matches ending in the suffix, judged per match so it cannot hide a real key on the same line. Only the aws-access-key rule uses it.
- internal/redact/scan_test.go: table test with runtime-built values (example ids, example secret, real id, mixed lines, longer tokens).
- internal/servecmd/serve_test.go: fixture changed to a runtime-built real-shaped id so the redaction test still exercises a hit.
- internal/worker/loop_test.go: `TestOnlyAddedLinesAreScanned` used the example id as its token; it now uses a real-shaped id.
- Redaction is unchanged; gitleaks is untouched.

## Testing
`make lint test` previously failed only in `TestOnlyAddedLinesAreScanned/a_new_literal`, because its token was now exempt. I changed the token and the test passes. I reran only that test, not the full suite.

## Open issues
- `go vet` printed a parse error for internal/roles/testdata/docs/skips/src/_x.go. It is in the earlier log, outside this change, and did not stop the tests.
