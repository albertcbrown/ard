# Triage

## What it does

Triage roles read an issue and write the plan for it. The `lead` routes the issue to the other triage roles it needs, and the `engineer` writes the implementation plan. Their sections together make up `plan.md`. When every requested triage role is done, and a human has added `ard:approved` (unless `require_approval` is off), the issue goes to a worker.

## Inputs and outputs

- **Inputs:** the issue and its comments (untrusted text), and earlier roles' output under `.ard/<issue#>/`.
- **Outputs:** the role's own file, `.ard/<issue#>/<role>.md`, and its own `## <section>` of `plan.md`. The designer may also write under `.ard/<issue#>/design/`.

## Labels

- `ard:pending` is the lead's request label. It is removed when the lead finishes.
- `ard:triage:<role>` requests a triage role; `:running` and `:done` follow the claim and the close-out.
- `ard:worker:<role>` is set once the plan is complete.
- `ard:approved` is the human go-ahead before a worker starts.
- `ard:needs-human` stops automation when a role reports it is blocked.

## Starter roles

- `lead`: routes the issue.
- `engineer`: writes the implementation plan.
- `product-manager`, `architect`, `designer`, `documentation-planner`: optional roles the lead may request.

## Config overrides

Set these under `overrides` for `triage/<name>` (see [Configuration](../configuration.md)): `after` (roles whose plan sections must exist first), `plan_section` (the `plan.md` heading the role owns), `model`, `timeout`, `sidecar`. `intervals.triage` sets the poll interval and `require_approval` the approval gate.
