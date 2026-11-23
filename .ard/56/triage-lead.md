---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds a kimi agent adapter next to the existing Claude adapter in the agent package and registers it, so a role can pick kimi through the per-role agent key that the config loader already parses. Following the owner's scope update and the decision recorded for #7, dsh-tui is left out. The adapter is Docker-only, bounds turns through its environment cap, and reports no cost or token figures.

## Routing
- triage:engineer: plan the new adapter package (command line, stream parsing, optional turns and cost), its registry wiring, Docker-only enforcement and credential passing, and parser tests from recorded output.
- worker engineer: Go code change with unit tests in the agent package and its wiring.

## Open issues
- dsh-tui is deferred by the issue owner and should be filed as its own issue once its engine and headless credential are ready. This issue's title still names both CLIs.
- The tests should parse recorded kimi output. If no scrubbed real capture exists yet, the worker can only add clearly named synthetic fixtures, and a human may need to record a real capture.
- I could not check the status of dependency #19 from here. It no longer appears among the open issues in docs/TODO.md, so I treated it as landed.
- The plan should name which model credential kimi needs and how the executor passes it, because today only the Claude credential handling is described.
