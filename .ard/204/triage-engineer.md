---
role: engineer
status: done
attempt: 1
---
Plans only the first of the issue's four PRs: the filing rules for LLM scanners (fingerprint grammar, near-duplicate skip, footer, autoclose and spend-floor config errors) plus the owner's stale-closer widening for scanner-label-only issues. Size M, risk low to medium. The route key, join rule and pre-filing guard already landed with the earlier issue, so the plan reuses them.

## Open issues
- PRs 2 to 4 (executor output dir, agent adapter, engine, serve wiring, CLAUDE.md) stay human-led and are not planned here; CLAUDE.md is a protected path.
- The pipeline still skips LLM scanners until PR 4, so the new pipeline behaviour can only be tested through helpers and execution-level tests.
- The new spend-floor config error rejects LLM scanner fixtures that exist today; the worker must update them.
