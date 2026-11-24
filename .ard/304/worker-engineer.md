---
role: engineer
status: done
attempt: 1
---
Adds the runbook for onboarding another repository and records in the proposal that limits and GitHub credentials belong to each `ard serve` process.

## Changes
- `docs/runbooks/onboard-repo.md` (new): the eight steps in order, with a worked two-repo example for `max_pods`, `budget_usd_per_day` and `max_budget_usd`.
- `docs/proposal.md`: a paragraph in Rollout (one serve per repo) and one in Configuration (per-process limits and credentials).
- `docs/layout.md`: the runbooks line now mentions repo onboarding.

## Testing
Docs only, so I ran no Go tests or linter. Checked by reading that the relative links point at existing files and anchors and that the example totals add up. Only the three docs files changed.

## Open issues
- ard has no token-cap setting, so the runbook says so and tells the operator to divide the subscription allowance through `max_pods` and the dollar caps. A maintainer may want a separate issue for a real token cap.
- I could not find the name of the deployment repo's `instances` entry, so the runbook words it generically and tells the operator to check that repo's docs.
- PAT support (#302), live cluster jobs (#54) and the shared roles repo (#59) are worded as conditional.
