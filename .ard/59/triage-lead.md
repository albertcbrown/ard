---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds a shared roles repository, pinned by ref the way sidecars are, as an extra source of role directories, with a repository's own directory of the same name taking precedence. It is a clear change to configuration and role loading, so it goes to the engineer to plan and implement.

## Routing
- triage:engineer: plan the new configuration key, the fetch and cache of the pinned roles repo (reusing the sidecar cache and ref rules), the merge order that lets local role directories win, and the tests.
- worker engineer: code, tests and the matching update to the Configuration section of docs/proposal.md.

## Open issues
- The change touches several packages (config, role discovery, executor caching) and adds a new configuration key. Normally the architect, product-manager and documentation-planner roles would plan it, but none of them exist in this repository yet (they are tracked as #40). A maintainer may want to wait for those roles or review the engineer's plan more closely.
- The issue depends on #11. That issue no longer appears among the open issues in docs/TODO.md, so it looks done. That is the only evidence I found.
- The acceptance check needs two repositories sharing one role set. Onboarding a second repository is tracked separately (#60), so the worker can probably only prove this with tests, not with a live second repository.
- Role directories are trust-sensitive: they are protected paths here. A maintainer should decide whether a role loaded from the shared repo gets the same human-approval gate, and who may change the pinned ref.
