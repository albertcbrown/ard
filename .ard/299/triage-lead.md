---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds one fixed line to every triage job's user message that lists each configured worker role and its network setting, built only from the resolved configuration. The dependency has already landed: the per-role network field is in the config loader (merged as part 1 in #330). The change is narrow and lives in the triage package, so only the engineer needs to plan it.

## Routing
- triage:engineer: steps for building the line in the triage run path from the resolved worker roles, plus a test with two worker roles (one with network on, one off) that checks the lead and other triage roles get the same line.
- worker engineer: code change in the triage package with unit tests.

## Open issues
- The user message tells the triage prompts what the worker can do. Once this lands, the owners may want to update the prompts under .ard/triage/ so they rely on the line. Agents may not change those files, so a person has to make that edit.
