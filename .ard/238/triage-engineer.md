---
role: engineer
status: done
attempt: 1
---
Adds a Window type and Result.Windows, parses the unified windows from every rate-limit event, and stores them in a new readings book in internal/limits. An observing parse wrapper in servecmd records and logs them for OAuth steps only, and the proposal paragraph is updated. Size M, risk low because nothing is gated and observe errors are only logged.

## Open issues
- The repository has no capture of the unifiedWindows payload, so its exact shape (object keyed by window or array, utilization scale) is assumed. The worker should parse tolerantly and a human should check a real capture.
- Two done-when items need a person: ticking the spike box on the parent issue, and watching a few days of real runs.
