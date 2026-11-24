---
role: engineer
status: done
attempt: 1
---
Plans the move of the filesystem half of the local executor into a new shared workspace package, plus an env-policy move into the executor package, with no behaviour change. The plan has ten ordered steps and keeps the existing local tests as the proof. Size L, risk medium because security-relevant code (scrub, the input symlink guard, rollback order) is moved.

## Open issues
- The issue body asks for a role-dir copy option, but the owner's latest design revision drops it, so the plan omits it. A maintainer should confirm.
- The Docker build-flags environment setting is already present in the local executor, so no change is planned for it.
- The size sits near the 2,000-line warning unless the behaviour tests and the git runner stay in the local package.
