---
role: engineer
status: done
attempt: 1
---
Plans a new internal snapshot package (no UI) that reads the pool, limits book, state and controller, adds a log-tee ring buffer, and adds per-role limited and budget refusal counters to the pool without changing its grants. The plan has six steps with table tests, a zero-GitHub-call test and a docs entry. Size L, risk medium because it touches the pool's offer path and wraps the serve logger.

## Open issues
- The parent design issue was not available to the planner, so field names and the panel details follow this issue's text. The worker should check them against the parent design.
- Running-job model and timeout come from the role config, since the job record stores neither.
- The issue cache invalidation is keyed on the latest job update time so close-out code stays untouched. A maintainer may prefer an explicit hook.
