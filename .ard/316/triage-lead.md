---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This part adds three hidden pod-side subcommands for the Kubernetes executor: pre, exec and post. Each one gets its own package, plus a small gitx helper and a layout doc line. The packages it builds on (tarball, jobpod and the shared workspace code) are already on main, so the work can start now. It goes to the engineer for planning and implementation.

## Routing
- triage:engineer: write the implementation steps and test plan against design revision 3. That revision replaces the earlier tarball-API plan in the comments. Check the plan against the jobpod and workspace code that is now on main.
- worker engineer: Go code for three new internal packages, the cli handler wiring, the gitx checkout helper and their tests.

## Open issues
- The issue body still describes the older fetch/exec/upload command set. The title and the latest revision describe pre/exec/post. The plan should follow revision 3 and ignore the superseded body.
- The comments estimate the size at L, about 980 lines including tests, and suggest moving the post command to a second PR if it runs long. A maintainer may want to approve that split before work starts.
- The signal handling and PID-1 reaping are hard to cover fully in unit tests. The later kind-based test is the real check.
- No architect, product-manager or documentation-planner roles exist in this repository, so only the engineer is routed.
