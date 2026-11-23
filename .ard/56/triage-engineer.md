---
role: engineer
status: needs-human
attempt: 1
---
No plan was written. A kimi adapter alone is small, but it cannot run a job today: the model credential is chosen once from the Claude adapter's variables and the executor refuses any other credential. Whoever settles the credential design and the missing inputs below makes this about size M (one new adapter package plus a small amount of wiring), with medium risk because it touches how credentials reach agent jobs.

## Open issues
- Credential decision: the kimi credential name and how it is selected are undecided. Today internal/servecmd/serve.go resolves a single credential through the Claude adapter's CredentialEnv, and internal/agentjob/command.go rejects any adapter whose PassEnv differs from that one credential. A role running on kimi needs its own credential, resolved per adapter, and the proposal's credential section needs a kimi paragraph.
- Docker-only: the adapter is pure and cannot see the executor mode. A maintainer should choose where the refusal to run kimi in host mode lives (the executor or agentjob) before a worker starts.
- The job image must contain the kimi binary. The Dockerfile and the images under deploy/ are protected paths needing human approval, and the install method and pinned version are undecided.
- Parser tests need recorded kimi stream-json output. The repository has no capture, and the schema is not documented in it. A human should record and scrub one (as is done for Claude), or accept clearly named synthetic fixtures.
- dsh-tui is deferred by the owner and should be filed as its own issue. The title of this issue still names both CLIs.
