---
role: engineer
status: done
attempt: 1
---
Adds the four remaining starter triage roles (product-manager, architect, designer, documentation-planner) as embedded prompts with starter config and an `after:` ordering, so `ard init` ships a full triage set.

## Changes
- `internal/roles/triage/{product-manager,architect,designer,documentation-planner}/prompt.txt`: new prompts, each owning its built-in plan section (Requirements, Design, UX, Docs); the designer may also write `.ard/N/design/`.
- `internal/roles/triage/lead/prompt.txt`: designer routing now includes command-line flags, output and messages.
- `internal/roles/config.yaml`: overrides for the four roles; ordering product-manager, architect; designer, documentation-planner; engineer after architect and documentation-planner.
- Tests: new `triage_roles_test.go` (load, model, `After`, section uniqueness, writable set, prompt shape, examples through `closeout.Parse` and `Build`); updated file lists in `roles_test.go` and `initcmd_test.go`; `engineer_test.go` expects the new `After`.
- `docs/proposal.md`: describes the roles, the ordering and the acceptance-criteria shape for #45. `roles.go`: comment only.

## Testing
Ran `go test ./internal/roles/... ./internal/initcmd/... ./internal/config/...`: all pass. `make test` was not run.

## Open issues
- `.ard/triage/` and `.ard/config.yaml` are not touched; copying the roles there for live sample runs is a human follow-up.
- Design-file restrictions (no script, no external references) are enforced by the prompt only.
