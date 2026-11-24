# Gate 2 runbook: first loop proven (#39)

Gate 2 proves triage, worker and review on real issues before the merger takes
over. ard runs live with the local executor on the owner's Mac, and a human
merges every PR. See [docs/proposal.md: Rollout](https://github.com/albertcbrown/ard/blob/main/docs/proposal.md#rollout).

**Done when** (from #39):

- 10 PRs opened by ard are merged by a human
- the executor failure rate is under 10% of agent jobs
- the go/no-go for Phase 2 is recorded on #39, with the cost per merged PR

Two stages: a smoke run on `albertcbrown/ard-scratch` that walks one issue
through every stage, then the gate itself on `albertcbrown/ard`.

> **After the merger landed (#46).** `ard serve` now runs the merger loop, so it
> squash-merges worker PRs that pass its gates and humans no longer merge them.
> To keep human merging for this gate, start `serve` without it:
> `ard serve --loops scanners,stale,triage,workers,reviewers,reaper`. A repo
> without rulesets (the private free-plan `ard-scratch`) merges on reviews alone
> after a two-minute grace period when the head has no check runs; with check
> runs, all must be green. Run `ard serve --dry-run` first to read the
> `merge.would_merge` lines. The merger's end-to-end test
> (`go test -tags integration ./internal/merger`, `ARD_IT_REPO` set) also needs a
> repo with rulesets plus CI for its red-check case (`ARD_IT_CI=1`): that case is
> optional and skipped on `ard-scratch`.

## Prerequisites

| What | Where |
|---|---|
| `ard` built from `main` | `make build`; record `./bin/ard version` (a behaviour change mid-gate is noted in the table) |
| GitHub App `ard-bot-albertcbrown`, installed on both repos | contents, issues and pull requests read and write, checks read and write, no `workflows` (#85) |
| App key | `~/.ard/keys/ard-scratch.pem`, mode 0600; 1Password ClaviumLabs / "ard-scratch GitHub App" |
| Model credential | subscription OAuth token, 1Password Private / "claude setup-token" (`claude setup-token`) |
| Extra usage off in the Claude account | otherwise a usage-limit stop may be billed as overage |
| Host tools | `claude` at `TestedVersion`, `git`, `go`, `make`, Docker running (the cve scanner has a Dockerfile), `osv-scanner`, `govulncheck`, `jq`, `sqlite3` |

Every agent role without a `Dockerfile` runs as a host subprocess and logs the
`executor.host_untrusted` warning (#84). That is expected on owner-controlled
repos until #55.

### Environment

Keep this in a file outside the repo and source it in the shell that runs ard:

```bash
# ard-env.sh
unset ARD_GITHUB_TOKEN ARD_GITHUB_PRIVATE_KEY ANTHROPIC_API_KEY
export ARD_GITHUB_APP_ID=5877373
export ARD_GITHUB_INSTALLATION_ID=184746846
export ARD_GITHUB_PRIVATE_KEY_FILE="$HOME/.ard/keys/ard-scratch.pem"
export ARD_AGENT_AUTH=oauth
CLAUDE_CODE_OAUTH_TOKEN=$(op read "op://Private/claude setup-token/credential") || return 1
export CLAUDE_CODE_OAUTH_TOKEN
```

`ARD_GITHUB_TOKEN` must stay unset: a personal token makes the client
read-only and is refused outside `--dry-run`.

## Stage A: smoke run on ard-scratch

Goal: one issue goes `ard:pending` → triage → approval → worker → draft PR →
eng-review, and a human merges it. About an hour and a few dollars.

1. **Repo.** `ard-scratch` holds a small Go package with `make test` and
   `make lint`. The worker runs `make test` (the starter default).
2. **Starter roles.** `./bin/ard init --repo albertcbrown/ard-scratch` syncs
   the labels and opens a PR on `ard-init`. Check it with
   `ard config check --repo albertcbrown/ard-scratch --ref origin/ard-init`
   from a clone, then **Squash and merge** it.
3. **Issue.** File one small, well-scoped issue and add `ard:pending`
   (intake is `label` mode, so only labelled issues enter).
4. **Run.** In a dedicated terminal (or tmux):

   ```bash
   . ./ard-env.sh
   ./bin/ard serve --repo albertcbrown/ard-scratch 2>&1 | tee -a ~/.ard/gate2-scratch.log
   ```

   `serve` creates its bare clone under `~/.ard/repos/` on first start.
5. **Watch.** Expected sequence on the issue:
   - `ard:triage:lead:running`, then a commit to `main` under `.ard/<n>/`
     and a close-out comment; `ard:pending` is removed
   - `ard:triage:engineer` → `:running` → `:done`; `plan.md` gains the
     engineer's section
   - a "ready for approval" comment. **Read `.ard/<n>/plan.md`**, then add
     `ard:approved` yourself (it counts only from a human with triage
     permission, added after triage finished)
   - instead of that comment, a needs-a-human comment when `plan.md` is missing
   - `ard:worker:engineer:running`, a `worker/<n>-…` branch and a draft PR
   - `ard:review:eng-review` on the PR, then a PR review and
     `ard:review:eng-review:done` or `:changes`
6. **Merge** the PR by hand once you are satisfied: the worker opens it as a
   draft and the controller marks it ready once every requested review is
   `:done` at the head, so just **Squash and merge**. (Mark it ready by hand
   only to override a review that is still `:changes`.) Stop `serve` with Ctrl-C (SIGINT); it stops cleanly
   between jobs.

**Moving faster than the poll.** Each loop ticks once at startup, then every
interval (triage 15 min, workers and reviewers 5 min). To act now, send
`SIGUSR1` to `serve` (`kill -USR1 <pid of ard serve>`): every loop runs one tick
immediately, still behind the kill switch and the job pool, and logs
`controller.wake`. No restart is needed.
`ard:approved` counts as soon as triage has finished, so there is no need to
wait for the "ready for approval" comment.

### Manual checks folded into the smoke run

| From | Check | How |
|---|---|---|
| #84 / #34 | host-mode warning | `executor.host_untrusted` WARN at startup and per agent job, and a `host_untrusted` row in `events` |
| #32 | diff guard | the worker's PR contains no `.ard/`, `.github/` or `.git` paths |
| #36 | reviewer never runs commands from the PR | the review's `## Checks` section lists only the repo's own lint and tests |
| #15 | kill switch | `touch ~/.ard/pause/albertcbrown/ard-scratch` stops new claims within one poll; `rm` resumes |
| #37 | Reaper | optional: kill a running agent's `claude` process; the Reaper fails the job within a minute, with one comment |

### Smoke run result (2026-11-23, ard 6432ff6)

[ard-scratch#61](https://github.com/albertcbrown/ard-scratch/issues/61) went
from `ard:pending` to a merged
[PR](https://github.com/albertcbrown/ard-scratch/pull/63) with five jobs and no
failures: lead $0.076 (Opus, 10 s), triage engineer $0.062, worker $0.059
(tests green first time), eng-review $0.055 (approve); about $0.25 in all. The
host-mode warning, diff guard, reviewer command scope and pause file behaved as
designed. Follow-ups: #156 (blocks stage B), #157 to #161.

## Stage B: the gate on albertcbrown/ard

0. **Host paths.** Committed output must not carry the controller's absolute
   paths, because `albertcbrown/ard` is public. #156 (merged) replaces them
   with placeholders; run a build that includes it.
1. **Starter roles.** `ard init` refuses without `--force` when `main`'s
   `.ard/config.yaml` differs from the starter, and `--force` would drop the
   dogfooding protected paths. For `albertcbrown/ard` the four agent roles and
   their overrides were added by hand from `internal/roles/` (#167). Then run
   `./bin/ard labels sync --repo albertcbrown/ard`.
2. **Ruleset bypass.** If `main` has a ruleset (pull request required, required
   status checks), add the App to its bypass list with mode **Always**
   (Settings → Rules → the ruleset → Bypass list). Without it every
   close-out's push to `main` fails with `GH013: Repository rule violations`
   and the job ends `failed`. Check it with
   `gh api repos/albertcbrown/ard/rulesets/<id> -q .bypass_actors`.
3. **Bot-commit audit.** Set the repository variable `ARD_BOT_LOGIN` to
   `ard-bot-albertcbrown[bot]`; without it `.github/workflows/bot-commits.yml`
   fails closed.
4. **Run** `serve` as in stage A with `--repo albertcbrown/ard`, in tmux or
   under launchd, and keep the log.
5. **Feed it** one or two small issues at a time (follow-ups and small
   Phase 2 items), each with `ard:pending`. Read each `plan.md` before adding
   `ard:approved`.
6. **Merge by hand.** Note whether you edited the code before merging.
7. **Record** one row per issue (below) as each PR merges or is abandoned.

## Measuring

The state database is `~/.ard/state/albertcbrown/<repo>/_controller.db`.

Agent jobs, failures and cost per issue:

```bash
sqlite3 -header -column ~/.ard/state/albertcbrown/ard/_controller.db "
  SELECT issue,
         COUNT(*)                                   AS jobs,
         SUM(status = 'failed')                     AS failed,
         printf('%.2f', SUM(COALESCE(cost_usd, 0))) AS cost_usd,
         GROUP_CONCAT(DISTINCT stage || ':' || role) AS roles
  FROM jobs WHERE stage != 'scanner'
  GROUP BY issue ORDER BY issue;"
```

Executor failure rate across the gate:

```bash
sqlite3 ~/.ard/state/albertcbrown/ard/_controller.db "
  SELECT printf('%d failed / %d jobs = %.1f%%', SUM(status = 'failed'), COUNT(*),
                100.0 * SUM(status = 'failed') / COUNT(*))
  FROM jobs WHERE stage != 'scanner' AND status != 'running';"
```

A review whose PR was merged, closed or branch-deleted before close-out ends
`stale`, not `failed`, so the query above does not count it.

A usage-limit stop (`limit_stop = 1`) is not an executor failure; list them
separately with `SELECT id, issue, fail_reason FROM jobs WHERE limit_stop = 1`.

Per-issue table, kept in the go/no-go comment:

| Issue | PR | Jobs | Failed | Cost (USD) | Human code edits | Merged | Notes |
|---|---|---|---|---|---|---|---|

## Go/no-go

Post one comment on #39:

```markdown
## Gate 2 result: GO | NO-GO

- Build: <ard version>, host: <Mac>, repo: albertcbrown/ard, dates: <from> to <to>
- Merged PRs: <n>/10
- Executor failure rate: <failed>/<jobs> = <x>% (target under 10%)
- Cost per merged PR: median <$>, max <$>; proposed `max_budget_usd` <$>, `budget_usd_per_day` <$>
- Human code edits before merge: <n> of <merged>

<per-issue table>

Problems found and the issues filed for them: ...
Decision and reasons: ...
```

On GO, tick the boxes on #39, close it, and set the budgets in
`.ard/config.yaml` from the measured cost.
