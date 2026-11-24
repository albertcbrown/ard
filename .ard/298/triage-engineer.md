---
role: engineer
status: done
attempt: 1
---
Adds `--network none` to the Docker run arguments for jobs marked NetworkNone and makes the worker mark its test step NetworkNone, and its agent steps NetworkAdvisory, when the role has network set to false. The plan has four steps, with table and worker tests. Size S, risk low to medium.

## Open issues
- The worker builds its own Jobs, so its agent steps do not carry the advisory marker today. The plan adds it in the worker's job builder.
- A role whose test command needs the network will fail once network is set to false. This is intended and should be noted in the PR.
- The config comments say the setting is advisory; the plan updates them. The rendered config strings stay as they are because agent steps remain advisory.
