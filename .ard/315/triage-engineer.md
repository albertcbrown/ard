---
role: engineer
status: done
attempt: 1
---
Adds scoped read-only installation token minting and revocation to the GitHub package, and a new leaf package holding the controller-to-pod contract (paths, Secret items, manifest, key helpers), with httptest and unit tests and a layout doc entry. It follows the newest design revision rather than the issue body. Size L (lower end), risk medium because it mints credentials.

## Open issues
- The issue body still describes the superseded controller job API with key table and TLS; a maintainer may want to update it. The plan does not create `internal/jobapi`.
- The plan comment's line reference for the proposal's sidecar-auth sentence may have drifted, so the worker should locate it by content.
