---
role: engineer
status: needs-human
attempt: 1
---
The change adds a Go-only missing-tests scanner role under internal/roles: a shell driver, a stdlib Go helper of about 400 lines, a Dockerfile, a starter config entry, fixtures, four test files, init tests and docs. It is size XL, well over 1,000 changed lines. The existing cve scanner's tests alone run to about 1,100 lines across three files. No plan was written, and a split is proposed below.

## Open issues
- Proposed split into two PRs, each independently green:
  - PR 1 (size L): the helper `missingtests.go` with its fixtures and `TestMissingTestsHelper*` tests (output decoding, fingerprint stability, limits, errors, vet). The helper is not yet wired into the starter tree, so the build stays unchanged.
  - PR 2 (size L): `scan`, the Dockerfile, the starter config entry, the roles and init test updates, the scan end-to-end test, the Docker integration test, and the proposal and CLAUDE.md updates.
- Once the split is accepted, the engineer can plan PR 1 first, then PR 2 after it merges.
- The CLAUDE.md Layout line is a protected path, so PR 2 needs human approval to merge.
- Enabling the scanner on ard itself (`.ard/scanners/`, `.ard/config.yaml`, `deploy/`) remains a separate human change, because agents may not edit those paths.
