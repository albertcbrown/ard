---
role: engineer
status: done
attempt: 1
---
Plans a documentation-only change: a new onboarding runbook with a worked two-repo limits example, a per-process limits and credentials note in the proposal's Rollout and Configuration sections, and a one-line layout tweak. Size M, risk low.

## Open issues
- No token-cap setting exists in the configuration or proposal today. The plan has the runbook cover the real keys (`max_pods`, `budget_usd_per_day`, `max_budget_usd`) and not invent a token-cap key; a maintainer may want a separate issue.
- PAT support, the shared roles repo and live cluster jobs are not landed, so the runbook words them as conditional.
- The `instances` entry name for the deployment repo could not be checked here; the worker should confirm it.
