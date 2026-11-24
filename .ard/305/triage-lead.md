---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a table-driven test proving that per-repository paths and the serve lock never collide across distinct repositories and agree for case variants of the same one, plus fixes for any collision the test finds. It is mostly test work over existing packages (repopath, state, controller, servecmd), so the engineer's plan is enough.

## Routing
- triage:engineer: list the path and lock functions to cover, the collision pairs, the lock-acquisition test under a temporary home directory, and how to handle any collision found.
- worker engineer: write the tests and any narrow fix in the path helpers.

## Open issues
- repopath already lowercases owner and name. If the test shows another root (for example the bare clone or state directory) is not case-folded, the fix changes where existing on-disk data is found. The plan should say whether existing mixed-case directories need a migration or fallback lookup. If that becomes a cross-package storage change, a maintainer may want an architect-level review, but no architect role exists here.
- The shared-roles cache is covered only if the issue it depends on has landed. The engineer should check whether it exists on main.
