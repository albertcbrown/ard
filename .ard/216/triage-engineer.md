---
role: engineer
status: done
attempt: 1
---
Adds the trufflehog starter scanner role (scan script, jq filter, Dockerfile, exclude list), its human-routed starter override, the init PR-body opt-out note and the starter listing tests, with a fake-binary test suite including a canary test. Size L, risk medium to high because the code handles live secrets.

## Open issues
- The route key from the dependency is already on main, so the work is not blocked.
- The init PR body is built in internal/initcmd/body.go, so a small non-test Go change is needed there.
- The worker has no network, so the trufflehog download checksums in the Dockerfile cannot be re-verified by it. A maintainer should check them against the signed release checksums.
- A maintainer should read the scan script and jq filter line by line before merge. CLAUDE.md is a protected path, so merge waits for human approval.
