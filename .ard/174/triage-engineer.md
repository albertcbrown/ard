---
role: engineer
status: done
attempt: 1
---
Makes the local executor's Cleanup restore owner write permission across the whole job home before removing it, without following symlinks. This covers the worker step cleanup and the Reaper, since both call Cleanup. It also sets a Go flag so future module caches are writable and adds tests and a docs note. Size S to M, risk low.

## Open issues
- The optional shared module cache is deliberately left out because a cache jobs can write to risks poisoning across jobs. A maintainer may want a separate issue for it.
- The worker step cleanup in internal/worker/run.go should be confirmed to go through Executor.Cleanup.
