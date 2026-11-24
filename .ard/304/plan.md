# Plan for #304

## Implementation

### Steps
1. Create `docs/runbooks/onboard-repo.md` (new), styled like `docs/runbooks/gate-2.md` (title with issue number, intro linking `../proposal.md#rollout`). Sections in this order, one per step of the issue, each with the exact commands:
   1. Install the GitHub App on the target repo; PAT is an alternative only once #302 lands (state it as conditional).
   2. `ard init --repo <owner>/<name>`; merge the `ard-init` PR with Squash or a merge commit (never rebase-merge; see the `ard init` lane in `docs/proposal.md` Guardrails).
   3. `ard labels sync`.
   4. Branch protection on `main` with the bot bypass.
   5. Copy `.github/workflows/bot-commits.yml` from this repo into the target repo by hand and set `ARD_BOT_LOGIN`. This is an operator action in the other repo; do not touch `.github/` here.
   6. Limits: `max_pods`, `budget_usd_per_day`, `max_budget_usd` (the real keys in `docs/proposal.md` Configuration) are per `ard serve`; ard does not coordinate across processes; the operator sets each so the total fits host, cluster and subscription. Worked example: two repos on one host and one subscription, e.g. host fits 4 concurrent jobs so each serve gets `max_pods: 2`; daily total of 60 USD split as 30 and 30 via `budget_usd_per_day`; per-job `max_budget_usd` unchanged. Token caps: no token-cap key exists in `.ard/config.yaml` or the proposal today. Do not invent one; describe the subscription's token limit as something the operator divides through `max_pods` and the dollar caps, and say so explicitly.
   7. Start with `--dry-run` (separate `~/.ard/state-dryrun/`), soak, then go live. In the cluster add an `instances` entry in the deployment repo; label live agent jobs in the cluster as needing #54 (conditional). Check the name `instances` against the deployment repo's docs before wording it.
   8. Optional `roles_repo: {repo, ref}` pointing at a shared roles repo (#59); roles otherwise live in the repo's own `.ard/`. Present as conditional on #59 landing, unless the proposal already says it is built.
2. Edit `docs/proposal.md`:
   - Rollout (`## Rollout`, near the "Where it runs" paragraph): add a short paragraph: one `ard serve` per repository; link to `runbooks/onboard-repo.md`.
   - Configuration (`## Configuration`, after the yaml block's `max_pods`/budget keys, near the Shared roles paragraph): state that `max_pods`, `budget_usd_per_day` and `max_budget_usd` are per process, must be set together across serves on one host or cluster, ard does not coordinate; each `ard serve` has its own GitHub credentials (App installation, or a PAT after #302).
3. Edit `docs/layout.md` line 57 only if needed: `docs/runbooks/` already exists, so add a mention of onboarding to the description (e.g. "operator runbooks (gate runs, live checks, repo onboarding)"). Keep it a one-line change.
4. Check that relative links resolve (`../proposal.md#configuration`, `../proposal.md#rollout`).

### Test plan
- Docs only; no Go tests. Run `make test` (and `make lint` if it exists) to confirm nothing else broke, and `git diff --stat` shows only the three docs files.
- Check by reading: the runbook has all eight steps in order, the multi-serve example sums to within the stated host, cluster and subscription totals; pending items (#302, #54, #59) are worded as conditional; no file under `.github/` or `.ard/` changed.
- Failure case: if a repo doc linter or link check exists in CI, run it on the new file.

### Risk
Low. Documentation only. No protected path in `merge.protected_paths` is touched (`docs/` is not listed). Main hazard is describing unlanded features (PAT, shared roles, cluster live jobs, token caps) as working.

### Size
S, about 120 to 180 added lines, though under the S threshold only if the runbook stays short; call it M if it exceeds 50 lines. Files: `docs/runbooks/onboard-repo.md` (new), `docs/proposal.md`, `docs/layout.md`.
