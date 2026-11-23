---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Cleaning up a job home fails when it holds Go's read-only module cache, so those trees are never removed. This is a clear bug fix in the local executor's step cleanup and the Reaper's retention cleanup, plus an optional decision on GOMODCACHE or GOFLAGS for job steps, and the engineer plans and implements it.

## Routing
- triage:engineer: plan a shared removal helper that handles read-only trees and does not follow symlinks, wire it into the executor's step cleanup (internal/executor/local) and the Reaper, and add tests for a 0555 directory with a 0444 file and for a symlink that points outside the home. Also decide whether to take the optional GOMODCACHE/GOFLAGS change.
- worker engineer: Go code change with unit tests, plus a docs/proposal.md update if the job environment changes.

## Open issues
- No architect or product-manager triage role exists. The engineer's plan therefore has to settle the optional shared module cache, including the cache-poisoning risk of a cache that jobs can write to and how it is mounted into Docker jobs. A maintainer may want to review that choice, or limit this issue to the cleanup fix and track the shared cache separately.
