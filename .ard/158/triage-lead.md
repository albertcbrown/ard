---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A small bug fix in the redactor: the env var that selects the agent auth mode holds a mode name rather than a credential, yet it is collected into the secret set, so a spurious warning is logged on every serve start and close-out. The change is confined to the redact package (the name-pattern match in FromEnviron in internal/redact/secrets.go) plus a test, so only the engineer needs to plan it.

## Routing
- triage:engineer: choose how to exclude the mode variable from the environment-based secret collection (e.g. an explicit non-secret allowlist) while keeping the real credential variables redacted, and specify the test that asserts no warning for it.
- worker engineer: narrow code change in internal/redact with a unit test.

## Open issues
- The secret-name match is a broad pattern that includes AUTH, so other non-secret variables with that substring would behave the same way. The planner should keep the exclusion narrow so no real credential stops being redacted.
- An earlier triage run for this issue failed at the push step in the controller, not in routing. A maintainer may want to confirm that push problem is resolved.
