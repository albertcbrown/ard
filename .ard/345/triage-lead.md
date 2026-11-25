---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Part C of the init and setup split changes `ard init` to write `.ard/` into the local checkout by default and keeps the `ard-init` PR behind `--pr`. It also drops label sync from init and adds the shared permission check. Both dependencies are now on main: the lock-based planner from #344 (merged as #380) and the `ard setup` skeleton from part D (merged as #379), which already syncs labels. So the work can start, and only the engineer planner and worker exist to take it.

## Routing
- triage:engineer: implementation steps for the local-tree mode, the `--pr` lane, removing label sync, the permission check report and the next-steps output, plus the test plan. Most of this is in `internal/initcmd`, and some may touch `internal/setupcmd` and `internal/cli`.
- worker engineer: code change in `internal/initcmd` with unit tests, and a docs touch-up.

## Open issues
- `docs/proposal.md` already describes the target behaviour. Its sentence saying init behaves as before until #344 and #345 land should be removed in this change.
- The shared permission check is tied to #302. `internal/setupcmd` takes permissions as an injected hook, so the planner should confirm a shared check exists that init can reuse. If none does, the planner should decide whether to factor one out here or wait for #302.
- The architect, product-manager and documentation-planner roles do not exist in this repository. The owner decisions on #303 cover the design.
