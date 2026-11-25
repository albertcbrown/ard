---
role: engineer
status: done
attempt: 1
---
Adds the three hidden pod-side commands (pre, exec, post) as separate packages with a small git checkout helper, cli wiring and tests, following the latest design revision rather than the older text in the issue body. Size L, risk medium because of signal handling, PID-1 reaping and the key-guarded HTTP endpoint.

## Open issues
- The shared workspace package lives at internal/executor/workspace on main; the plan uses that path.
- Signal and reaping behaviour is only partly unit-testable; the later kind-based test is the real check.
- If the work runs long, a maintainer may want job-post moved to a second PR.
