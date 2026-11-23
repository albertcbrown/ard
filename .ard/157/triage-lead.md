---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
When a non-lead triage role claims a human-filed issue that was already admitted, the claim log reports the wrong reason. The fix is small and internal: add a reason value to the intake package, use it on the stored-admission path in the triage candidate builder, and add a test case. Admission behaviour and user-facing interfaces stay the same, so only the engineer needs to plan it.

## Routing
- triage:engineer: steps for the new reason constant, setting it on the stored-admission path in internal/triage/candidates.go, and the candidates test case.
- worker engineer: small Go change with a unit test.

## Open issues
- The new reason string will appear in slog output. Check whether any docs or log-parsing tooling list the reason values.
- A comment on the issue shows that an earlier lead attempt failed while pushing. That was a controller problem, not a problem with this routing.
