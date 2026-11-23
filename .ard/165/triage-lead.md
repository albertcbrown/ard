---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This asks for a startup check so that `ard serve` and `ard config check` refuse to run on a git older than the minimum the committer needs, with a message naming the version found and the minimum. The request is clear and self-contained, so the engineer plans it and the engineer worker implements it.

## Routing
- triage:engineer: find the minimum git version, decide where the version check lives and which commands call it, and write the test plan.
- worker engineer: add the git version check, the error message and the tests.

## Open issues
- The exact minimum version is still unknown: 2.47.3 passes and 2.39.5 fails. The plan needs a way to pin it down, or else a conservative choice that a maintainer agrees to.
- Making the failing fetch work on older git is listed as optional. It would be best handled as a separate follow-up.
- This changes what users see when ard starts, and no product-manager or documentation-planner role exists. The engineer's plan should cover any documentation updates.
