# ard

ard makes a GitHub repository maintain itself. One `ard` process per repository runs a set of loops that share a small pool of job slots (`max_pods`, default 3). A deterministic controller owns every GitHub and git side effect. Agents only read and write files in a worktree.

## The loop

1. **Scanners** find problems (CVEs, lint, missing tests, docs gaps, secrets) and file deduplicated issues.
2. **Triage** roles (lead, engineer, and optionally product manager, architect, designer, documentation planner) read an issue and write `plan.md`.
3. **Workers** implement the plan on a `worker/...` branch, run the tests and open a draft pull request.
4. **Reviewers** (engineering, security, license, product) critique the pull request and ask for fixes.
5. **The merger** squash-merges a pull request once every review passed and its checks are green.
6. **The Reaper** marks dead or overdue jobs `ard:executor:failed`.

`.ard/<issue#>/` in your repository holds each issue's plan, per-role summaries and full logs.

## The `ard:` labels

GitHub labels prefixed `ard:` are the queue and the state machine. Most follow `ard:<stage>:<role>[:<state>]`, for example `ard:triage:lead:requested`. Run `ard setup` to create them. The ones you will touch:

| Label | Meaning |
| --- | --- |
| `ard:pending` | Ready for triage. A maintainer adds it in `label` intake mode |
| `ard:approved` | Human go-ahead for a worker to start |
| `ard:needs-approval` | The pull request touches a protected path and waits for a human |
| `ard:needs-human` | Blocked; automation stops until a human acts |
| `ard:hold` | The controller ignores this issue or pull request |
| `ard:executor:failed` | A job died or ran over time; remove the label to retry |
| `ard:tests:failed` | The worker could not get the tests green |
| `ard:keep` | Keep failed-job resources past 24 hours |
| `ard:pause` | On the status issue: stop all new claims |

The full state machine is in the [design proposal](https://github.com/albertcbrown/ard/blob/main/docs/proposal.md).

## What humans still decide

- **Approval.** With `require_approval` on (the default), a worker starts only after a human adds `ard:approved`.
- **Protected paths.** A pull request touching any `merge.protected_paths` entry (for example `.github/`) waits for a human approval.
- **Merges.** The merger lands pull requests that pass every gate. To keep merging by hand, start `ard serve` without the `merger` loop (see [Operations](operations.md)).
- **Workflows.** Agents cannot edit `.github/workflows/`; a person does.

Next: [Install](install.md), then [Getting started](getting-started.md). The design source of truth is the [proposal](https://github.com/albertcbrown/ard/blob/main/docs/proposal.md).
