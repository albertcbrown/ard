---
role: engineer
status: done
attempt: 1
---
Replaces the conflict and force handling in the init planner with a lock-driven upgrade planner, adds a tolerant lock reader and writer, and lets the init path check and the starter's protected paths accept the lock. The plan has eight steps with table tests, including a hand-edited lock. Size M, risk medium because it removes a CLI flag and changes the init path gate.

## Open issues
- The `--force` flag is removed, since the planner replaces it. Maintainers should confirm that is intended.
- The starter template gains the lock in its protected paths. This repository's own `.ard/config.yaml` is not touched, so a maintainer must add the entry there by hand if wanted.
- The ard version needs threading from the CLI app into the init handler. The worker should pick the smallest route.
