---
role: engineer
status: done
attempt: 1
---
Adds a fail-closed PII pass (email, phone, IP with an allow-list) to the shared redactor, runs scanner job logs through the close-out redactor locally at job end, and covers the worker and scanner paths that bypass it. It also adds a commit-path test and the proposal edits. Size L, risk medium because every close-out uses the redactor.

## Open issues
- Policy call to confirm: commit-author emails in git log output are treated as PII and replaced, except noreply GitHub and Anthropic addresses.
- A PII false positive (for example a version string shaped like an IP) can alter committed text. A line over 1 MiB fails closed and keeps the log host-local.
- Flagged or failed scanner logs stay raw on the host with a marker file. The existing 7-day sweep must be extended to remove the marker.
