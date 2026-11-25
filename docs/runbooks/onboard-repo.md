# Onboard another repository (#304)

How to run ard on a second repository, by hand and in order. The model is one
`ard serve` per repository. See
[docs/proposal.md: Rollout](../proposal.md#rollout) and
[Configuration](../proposal.md#configuration).

Each `ard serve` has its own GitHub credentials (a GitHub App installation, or a
fine-grained PAT after #302), its own state directory and its own limits. ard
does not coordinate across processes. Roles live in the repository's own
`.ard/`.

## 1. Install the GitHub App

Install the App on the target repository only. A fine-grained PAT is an
alternative only once #302 lands; until then use the App.

## 2. Initialise the repository

```sh
ard init --repo <owner>/<name>
```

This writes the starter `.ard/` tree locally. Commit it on a branch and merge
it with **Squash** or a **merge commit**, never fast-forward or rebase (see the
`ard init` lane in [Guardrails](../proposal.md#guardrails)). Nothing takes
effect until it is merged, because `ard serve` reads `origin/main`. Use
`ard init --pr` to have ard open the `ard-init` PR.

## 3. Run setup

```sh
ard setup --repo <owner>/<name>
```

This creates the labels and checks permissions, the ruleset and App bypass, App
permissions and merge settings. `ard setup --check` only reports and exits 2 on
drift.

## 4. Fix the manual rows

Each `manual: <name>: <fix>` row carries the exact fix. Typical ones:

- `ruleset`: protect `main` with a ruleset and put the App on its bypass list
  (mode Always). The committer pushes `.ard/<issue#>/` commits straight to
  `main`; see "Branch protection and the bot" in the proposal.
- `merge-settings`: turn off rebase merging.
- Bot-commit audit, by hand in the target repository (do not edit `.github/`
  here): copy `.github/workflows/bot-commits.yml` from this repository, then set
  the repository variable `ARD_BOT_LOGIN` to the App's `<slug>[bot]` login.

Rerun `ard setup` until it is green.

## 5. Set this serve's limits
`max_pods`, `budget_usd_per_day` and `max_budget_usd` in `.ard/config.yaml` apply
to this `ard serve` alone. When a host or cluster runs more than one, set each
serve's limits so the **total** fits the host, the cluster and the model
subscription.

Worked example: two repositories, A and B, share one host and one subscription.
The host runs 4 jobs at once and the budget for the subscription is 60 USD a day.

| Setting | Repo A | Repo B | Total |
|---|---|---|---|
| `max_pods` | 2 | 2 | 4 (the host's capacity) |
| `budget_usd_per_day` | 30 | 30 | 60 (the daily budget) |
| `max_budget_usd` (per job) | 5 | 5 | at most 4 jobs x 5 = 20 in flight |

If A is the busier repository, give it more of both, but keep each column's sum
within the total.

**Token caps.** ard has no token-cap setting today, and this runbook does not
invent one. Divide the subscription's token allowance through `max_pods` and the
dollar caps above. Each serve records a usage limit on its own, so one serve
hitting the subscription limit does not stop the other until it hits it too.

## 6. Dry run, soak, go live

```sh
ard serve --dry-run
```

Dry-run keeps its own state in `~/.ard/state-dryrun/<owner>/<repo>/` and writes
intended effects to `effects.jsonl`. Soak and read the ledger, then start
`ard serve` without the flag.

In the cluster, add an entry for the repository to the instances list in the
deployment repository (check the key name in that repository's docs). Live agent
jobs in the cluster need #54; until it lands, run the cluster instance in
dry-run only.

## 7. Optional: a shared roles repository

`roles_repo: {repo: <owner>/<name>, ref: <ref>}` in `.ard/config.yaml` loads
roles from a shared repository, and the repository's own role of the same name
wins. Pin a full commit SHA. The shared roles repository is tracked in #59; use
this only where it is available. See
[Configuration](../proposal.md#configuration).
