---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds the first version of the `ard setup` command: a new setup package, CLI wiring, a check-then-apply flow with `--check` and `--dry-run`, the shared permission check, and `ard:*` label sync that reuses the existing labelsync code. Its stated dependency, the #343 design docs, has already landed on main, so the work can start now. Only the engineer triage role exists, so it plans the whole change.

## Routing
- triage:engineer: implementation steps for the new setup package, the CLI handler and main.go wiring, the report rows and exit codes, the shared permission check, the label-sync reuse with the `ard labels sync` alias, and idempotency tests against the GitHub fake.
- worker engineer: Go code and tests across a new internal package, internal/cli, the main command and internal/labels/labelsync.

## Open issues
- The shared permission check is also part of #302, which has a plan but has not landed. The engineer plan should say whether this issue creates the check or waits to reuse the one from #302, so the two branches don't build it twice or conflict.
- This change adds a user-visible command and touches several packages. No product-manager, architect or documentation-planner role exists, so the engineer plan has to cover those concerns. The design is already settled in docs/proposal.md, so the docs should need little or no change.
