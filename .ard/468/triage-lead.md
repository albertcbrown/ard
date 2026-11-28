---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is a clear, owner-specified hardening change. Scanner job logs will go through the existing close-out redactor before they stay on disk, and the redactor gains a fail-closed PII pass with an allow-list. Every path from a log into the repository is audited, and docs/proposal.md is updated to match. Only the engineer triage role exists, so it writes the whole plan, and the engineer worker implements it.

## Routing
- triage:engineer: list every commit path for logs and log-derived text, and design the PII patterns, allow-list and placeholders. Decide whether commit-author emails count as PII. Plan the scanner-log redaction hook, the fail-closed behaviour, the table-driven offline tests and the proposal edits.
- worker engineer: code change in the redactor and scanner run path, with tests and the docs/proposal.md update.

## Open issues
- The change spans several packages (close-out redactor, scanner runner, close-out commit paths), but no architect role exists. The engineer plan should settle the approach.
- No product-manager or documentation-planner role exists either. The proposal update is in scope for the worker.
- The plan must make one policy call, the issue's open question of whether commit-author emails in git log output count as PII. A maintainer may want to confirm that choice.
- The optional gitleaks pass and the PII pass both fail closed, so a PII false positive could block commits. Allow-list tuning deserves a careful review.
