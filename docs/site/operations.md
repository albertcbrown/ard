# Operations

## Running and stopping

`ard serve` runs every loop. `ard serve --loops scanners,triage` runs only the named loops, which is how you keep merging by hand: leave out `merger`. A dry run (`ard serve --dry-run`) logs every side effect to a JSONL ledger instead of performing it.

Send `SIGUSR1` to the process to make every loop run one tick now (waking loops, #161).

## Kill switch

Any one of these pauses all new claims (running jobs finish):

- set `paused: true` in `config.yaml` on `main`;
- add the `ard:pause` label to the status issue.

Remove it to resume. The costs loop keeps running while paused.

## The status issue

ard keeps one pinned status issue per repository. It shows what each loop last did, and it carries `ard:pause`.

## Logs and state

- **Logs.** Every agent run's full output is committed to `.ard/<issue#>/logs/<stage>-<role>-<timestamp>.log`, after redaction. The controller logs to stderr as structured lines; `--log-level debug` shows more.
- **State directory.** `~/.ard/` on the host holds the config snapshots (`config/`), scanner state (`state/<owner>/<repo>/<scanner>.db`) and job worktrees. It is a cache: GitHub is the truth.

## The Reaper and failure causes

The Reaper runs every minute. It marks a job that died or ran over time `ard:executor:failed` with a cause, keeps its worktree and log for 24 hours (`ard:keep` extends that) and cleans up. Causes:

| Cause | Meaning |
| --- | --- |
| `heartbeat_quiet` | No log output for 10 minutes |
| `deadline` | The job ran past its `timeout` |
| `controller_restart` | The job was orphaned across a restart |
| `no_process` | The job never started or its process vanished |
| `rate_limit` | The agent reported a transient API error; retried once automatically |
| `clone_failed` | Preparing the worktree failed at git or clone |
| `usage_limit` | The agent's usage limit is exhausted |

To retry a failed job, remove `ard:executor:failed` from the issue.

## Usage limits

When an agent reports its usage limit is exhausted, ard records it and stops starting jobs for that agent until the limit resets, instead of failing each job. `budget_usd_per_day` and `max_budget_usd` cap spending in dollars.

## The cost table

ard writes a cost table into each issue body, between ard-owned markers, from recorded usage (#172). Do not edit it by hand; ard rewrites it.

## Upgrades

Build the new `ard`, stop `ard serve` and start the new binary. Run `ard config check` first, because a new release may reject a key the old one accepted. Review the release for changes to the starter roles; `ard init` proposes updates as a pull request and never deletes.

## Backups and rebuilding

Everything that matters is in GitHub (issues, labels, `.ard/`). `~/.ard/state/` can be deleted. To rebuild a scanner's state from its issues, stop `ard serve` and run:

```sh
ard rebuild-state cve
```

To rebuild a damaged clone, stop `ard serve`, delete the damaged clone under `~/.ard/`, and start `ard serve` again. Nothing in the clone is the only copy of anything.

## Runbooks

The [Gate 2 runbook](operations/gate-2-runbook.md) is the checklist used to prove the first loop on real issues.
