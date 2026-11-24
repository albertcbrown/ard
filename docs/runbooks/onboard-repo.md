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

This opens an `ard-init` PR. Merge it with **Squash** or a **merge commit**,
never a rebase merge (see the `ard init` lane in
[Guardrails](../proposal.md#guardrails)).

## 3. Sync labels

```sh
ard labels sync
```

## 4. Branch protection on `main`

Protect `main` with a ruleset and put the App on its bypass list (mode Always).
The committer pushes `.ard/<issue#>/` commits straight to `main`; see "Branch
protection and the bot" in the proposal.

## 5. Copy the bot-commit audit

By hand, in the target repository (do not edit `.github/` here): copy
`.github/workflows/bot-commits.yml` from this repository, then set the
repository variable `ARD_BOT_LOGIN` to the App's `<slug>[bot]` login.

## 6. Set this serve's limits

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

## 7. Dry run, soak, go live

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

## 8. Optional: a shared roles repository

`roles_repo: {repo: <owner>/<name>, ref: <ref>}` in `.ard/config.yaml` loads
roles from a shared repository, and the repository's own role of the same name
wins. Pin a full commit SHA. The shared roles repository is tracked in #59; use
this only where it is available. See
[Configuration](../proposal.md#configuration).
