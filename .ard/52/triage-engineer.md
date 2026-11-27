---
role: engineer
status: done
attempt: 1
---
Adds the starter scanner role `scanners/architecture` (an LLM prompt, its starter config entry, embed and init updates, and tests that check the prompt's worked examples against the fingerprint grammar). Size S to M, risk low to medium because the prompt ships to every adopter and runs unattended in host mode.

## Open issues
- A maintainer should read the shipped prompt before merging; `internal/roles/` is not a protected path, so nothing forces that review.
- Adding the scanner to this repository's own `.ard/scanners/` and `.ard/config.yaml` touches protected paths, so a human has to do it after a dry-run smoke run. It is not part of this plan.
- Step 2 assumes the config loader accepts `model` and `max_budget_usd` on a scanner override; the worker should confirm this with the new load test and report if it does not.
