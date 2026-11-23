---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change adds an exact-name exclusion for ARD_AGENT_AUTH in FromEnviron, as plan.md and the issue describe. The new test covers the exclusion, the lowercase name, the real credentials still being redacted, and MY_AUTH still warning. Approved.

## Checks
- `go test ./internal/redact/...`: passes.
- `go vet ./internal/redact/` and `gofmt -l internal/redact`: clean.
- Diff coverage: every added line in secrets.go is executed by the new test. I did not run a separate coverage profile.
