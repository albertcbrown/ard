---
role: engineer
status: done
attempt: 1
---
Keeps the type label as the only source of the PR prefix and drops the prefix when no label exists, so the title becomes the issue title plus its number. The plan has four steps across the worker and committer packages, their tests and the proposal. Size S, risk low.

## Open issues
- Taking the prefix from the lead's routing was rejected because it needs a change to the lead prompt under .ard/triage/, which agents may not edit.
- Unlabelled issues now get unprefixed titles, so maintainers who want a prefix must apply a type label at intake.
