---
role: engineer
status: done
attempt: 1
---
Adds a locally built agent image (Dockerfile and test script under deploy/), two Makefile targets, a pin-parity unit test tied to the Claude adapter's tested version, and the proposal text. Size M, risk medium-low.

## Open issues
- The change touches deploy/, a protected path, so merge needs human approval.
- The Docker build and image test cannot run in the worker's environment. A human with Docker must run both Makefile targets and post the PASS lines.
- The worker needs the real release checksums for the pinned CLI version. If it cannot get them, it should stop rather than guess.
