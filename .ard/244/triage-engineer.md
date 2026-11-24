---
role: engineer
status: done
attempt: 1
---
Adds image-ref validation, an `AgentImage` option in the local executor that runs untrusted-input roles without a Dockerfile under Docker (with no host fallback), a read-only clone mount, the changed host warning, tests and a proposal update. The plan has ten steps. Size L, risk high because it decides container versus host isolation.

## Open issues
- The plan's step 5 and step 7 contain choices the worker must make from the code (whether to mount the clone for all Docker jobs, and how to filter the host warning). A maintainer should review the no-fallback path closely.
- The Docker integration test and the live Mac check need a person.
