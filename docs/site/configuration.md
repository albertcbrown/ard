# Configuration reference

ard is configured by convention. A role exists because its directory exists, and `.ard/config.yaml` holds only global settings and overrides. Anything unset takes the default below. Check your file with `ard config check`; `ard serve` reads it from `origin/main`, so a change takes effect once it is merged. The full design is in the [proposal](https://github.com/albertcbrown/ard/blob/main/docs/proposal.md).

Durations are Go-style strings such as `30m` or `4h`; `d` is not accepted, so write `720h` for 30 days. No loop interval may be under `1m`.

## Global keys

| Key | Default | Meaning |
| --- | --- | --- |
| `max_pods` | `3` | Job slots shared by all loops |
| `executor` | `local` | Where jobs run |
| `agent` | `claude` | The agent CLI adapter |
| `require_approval` | `true` | A worker starts only after a human adds `ard:approved` |
| `budget_usd_per_day` | `50` | Daily dollar cap for the repository (UTC day) |
| `tokens_per_day` | unset | Default daily token cap per role (`40M`, `1.5M`, `500k` or an integer). Tokens are recorded now; no cap is enforced yet. A job that reports no tokens is not counted and is marked on the issue |
| `max_budget_usd` | `5` | Dollar cap for one job |
| `global_max_open` | `30` | Cap on open ard-filed issues across all scanners |
| `paused` | `false` | Kill switch: no new claims while true |

## Loop intervals

| Key | Default | Meaning |
| --- | --- | --- |
| `intervals.scanners` | `4h` | How often scanners run |
| `intervals.triage` | `15m` | Triage loop poll |
| `intervals.workers` | `5m` | Worker loop poll |
| `intervals.reviewers` | `5m` | Reviewer loop poll |
| `intervals.merger` | `5m` | Merger loop poll |
| `intervals.prwatch` | `5m` | PR watch loop poll (minimum `1m`): checks ard's open pull requests for human merges, closes without merge, long-pending checks and red drafts |
| `intervals.approvals` | `1m` | Approval watch poll (minimum `1m`): one cheap search for a human adding `ard:approved` or removing `ard:needs-human` or `ard:executor:failed`; a hit wakes the loop that owns the item, so the triage interval can stay long |
| `pr_watch.pending_timeout` | `2h` | How long a required check may stay pending before the PR watch flags it (at least the interval, at most `7d`) |

The Reaper runs every minute and is not configurable.

## Intake

| Key | Default | Meaning |
| --- | --- | --- |
| `intake.mode` | `label` | `label`: only issues a maintainer labels enter triage. `all`: every human-filed issue enters |
| `intake.label` | `ard:pending` | The label that admits an issue in `label` mode. It may not be one of ard's own label names |

## Merge gate

| Key | Default | Meaning |
| --- | --- | --- |
| `merge.protected_paths` | none | A pull request touching any entry waits for human approval. There is no built-in list: removing an entry removes its gate. An entry ending in `/` is a directory prefix, anything else is a glob |

`ard init` writes a recommended list that includes `.github/`, `.ard/config.yaml`, `.ard/ard-init.lock`, the role directories, `CODEOWNERS`, `CLAUDE.md`, `AGENTS.md`, `.claude/`, `.mcp.json` and build and deploy files.

## Stale scanner issues

| Key | Default | Meaning |
| --- | --- | --- |
| `stale.inactive_for` | `720h` | A scanner issue untouched this long is commented on and closed |
| `stale.suppress_for` | `2160h` | How long the closed finding stays suppressed |

## Redaction

| Key | Default | Meaning |
| --- | --- | --- |
| `redact.gitleaks.enabled` | `false` | Also scan logs with gitleaks before they are committed |
| `redact.gitleaks.path` | `gitleaks` | The gitleaks binary |
| `redact.gitleaks.block_autoclose_when_missing` | `false` | Refuse to autoclose when gitleaks is enabled but missing |

## Sidecars

A sidecar is a read-only reference repository mounted for a role. `<name>` is yours to choose.

| Key | Default | Meaning |
| --- | --- | --- |
| `sidecars.<name>.repo` | required | `owner/repo` of the sidecar |
| `sidecars.<name>.ref` | `main` | Branch, tag or commit to mount |
| `roles_repo` | none | Optional shared roles repository: `{repo: owner/name, ref: <branch, tag or commit>}`. Its role directories load beneath the repo's own `.ard/`, and a local role of the same name wins. With no `ref` it follows the shared repo's default branch |

## Overrides

`overrides` maps a role path (`scanners/cve`, `triage/lead`, `workers/engineer`, `reviewers/eng-review`) to a mapping of the keys below. A role may also set `agent`, `max_budget_usd`, `budget_usd_per_day` and `tokens_per_day` to override the global values. An unknown key is an error.

| Key | Default | Applies to | Meaning |
| --- | --- | --- | --- |
| `overrides` | none | all | The mapping itself |
| `every` | global `intervals.scanners` | scanners | Run interval for this scanner (an LLM scanner needs at least `24h`) |
| `max_open` | `5` | scanners | Open issues this scanner may have |
| `group_size` | `10` | scanners | Findings grouped into one issue |
| `autoclose` | `false` | scanners | Close the issue when the finding disappears (an error on an LLM scanner) |
| `route` | `pending` (executable) or `none` (LLM) | scanners | Where filed issues go: `pending` enters triage, `none` files with the scanner label only, `needs-human` files with `ard:needs-human` |
| `model` | adapter default | agents | Model name passed to the agent |
| `timeout` | `30m` | all roles | Job deadline |
| `sidecar` | none | agents | Name of a `sidecars` entry to mount |
| `after` | none | triage | Roles whose plan sections must exist first |
| `plan_section` | none | triage | The `plan.md` heading the role owns |
| `tests` | `make test` | workers | Command the worker runs before opening a PR |
| `max_test_fixes` | `2` | workers | Fix attempts after a red test run |
| `max_review_rounds` | `3` | workers | Review rounds before `needs-human` |
| `reviewers` | all reviewer roles | workers | Reviewer roles requested for this worker's PRs |
| `diff_warn_lines` | `2000` | workers | Changed lines above which the PR carries a large-change note |
| `diff_max_lines` | `3000` | workers | Changed lines above which the change is held for a human |
