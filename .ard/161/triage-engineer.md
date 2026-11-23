---
role: engineer
status: done
attempt: 1
---
Adds a wake channel to the controller, fed by SIGUSR1 in ard serve, so every loop runs one tick immediately through the normal kill-switch and pool path, logged as controller.wake. It also updates the proposal and the Gate 2 runbook. Size M, risk low.

## Open issues
- The signal is Unix only; other platforms get no wake, and ard already supports only macOS and Linux.
