---
role: engineer
status: done
attempt: 1
---
Adds a read-only Bubble Tea terminal UI to ard serve in a new internal/ui package. It includes TTY detection with --ui and --no-ui flags, a capped serve.log, sanitised panel rendering from the snapshot, and the keys the issue lists. The plan has seven steps with golden-frame and key tests. Size L, risk medium.

## Open issues
- The design issue it builds on was not readable from this checkout, so panel wording and key details follow the issue slice. The worker should read that design before rendering.
- The snapshot package is already present, but a maintainer should confirm its dependency has merged.
- Two new third-party modules are added. Check the pinned versions, the module paths and that CGO stays off.
- The logger is built before serve starts, so redirecting logs to the file needs the log level passed through. The plan suggests rebuilding it with cli.NewLogger.
