---
role: engineer
status: done
attempt: 1
---
Adds a read-only history mount to scanner jobs (a new Job field, Docker mount and environment variable), a marker file by which a scanner asks for it, and a new built-in history scanner that reuses the HEAD scanner's fingerprint scheme and fail-closed checks. Size L, risk medium because it changes the job sandbox and handles raw secrets in pipes.

## Open issues
- A maintainer should confirm the read-only mount of the controller's bare clone over a bundle before the worker starts. The mount exposes every fetched ref, including unmerged PR branches.
- Both scanners use the same fingerprint, but each scanner role keeps its own snapshot. Whether a value found by both becomes one issue depends on the scanner engine's cross-role handling, which this plan does not change. A maintainer should say if cross-role deduplication is required.
- Enabling the scanner in this repository needs a maintainer-made copy under the protected scanners directory and an entry in the protected config file. Agents may not make those edits.
- No Kubernetes executor exists yet, so only a comment documents the contract it must meet.
