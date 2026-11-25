---
role: engineer
status: done
attempt: 1
---
Plans edits to the five starter prompts (worker, triage lead, triage engineer, eng-review, security-review) for per-role network access, a new prompt-content test file pinning the key sentences, and matching updates to the starter-role paragraphs in the proposal. Size M, risk medium-low because the prompts ship to every repository that runs the init command.

## Open issues
- The worker prompt moves from a blanket no-network rule to one that depends on the Network line, so existing prompt tests that pin the old wording will need deliberate updates.
- The live role prompts under .ard/ are protected and untouched; they need a separate human PR.
