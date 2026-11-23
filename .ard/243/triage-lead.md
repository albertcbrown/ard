---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a locally built default agent image: a Dockerfile and test script under deploy/, two Makefile targets, a pin-parity unit test against the Claude adapter's tested version, and matching proposal text. The design is already settled in the parent issue's implementation plan, and engineer is the only triage planner in this repository, so engineer plans it and the engineer worker builds it.

## Routing
- triage:engineer: implementation steps for the Dockerfile, test script, Makefile targets, pin-parity test and proposal edits, following the parent issue's 55a plan.
- worker engineer: code, test and docs change. The pin-parity test runs under make test. The Docker build does not.

## Open issues
- The change touches deploy/, which is a protected path in .ard/config.yaml, so the pull request waits for a human approval before merge.
- The Docker build and image test cannot run in the worker's test run. A human with Docker must run both Makefile targets and post the PASS lines before the first done-when box is checked.
- No architect or documentation planner role exists here. The parent plan already covers the design and the proposal wording, so eng-review should check the proposal edits against it.
