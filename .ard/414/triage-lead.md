---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
Adds a read-only terminal UI to ard serve that draws the slice 1 snapshot, sends logs to a file while it runs, and keeps today's output when there is no TTY. The engineer triage role writes the plan and the engineer worker implements it. This repository has no architect, designer or documentation triage role, so the engineer plan has to cover the new dependency, the layout and the proposal paragraph too.

## Routing
- triage:engineer: implementation steps for TTY detection, log redirection, panel rendering, key handling and sanitisation, plus the golden-frame and key-handling test plan and the dependency pins.
- worker engineer: Go code, tests and the proposal paragraph, checked with make lint test.

## Open issues
- The issue says it depends on #413. The snapshot package (internal/snapshot) is already in this checkout, so the work can start, but a maintainer should confirm #413 has merged before the worker runs.
- The change adds two new third-party modules (Bubble Tea v2 and Lip Gloss v2) along with their transitive dependencies. The issue records that as the owner's decision. eng-review should still check the pinned versions, the module paths and that CGO stays off.
