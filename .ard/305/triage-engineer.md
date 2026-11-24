---
role: engineer
status: done
attempt: 1
---
Adds a table-driven test in internal/servecmd proving that every per-repository root (state, lock, bare clone, work, logs, config snapshot, shared-roles cache) is distinct for colliding-looking pairs and identical across case variants, plus a lock test for two repos and for the same repo. Every root already goes through the shared path helper, so no collision is expected. Size M, risk low.

## Open issues
- No existing on-disk layout is expected to change. If the test does find a collision, the fix could move existing data, so the worker should stop and ask for a maintainer decision on migration.
