---
role: engineer
status: done
attempt: 1
---
Replaces the conflict/`--force` logic in `ard init` with an upgrade planner driven by `.ard/ard-init.lock` (version plus path → sha256 of each starter file written). Files the user edited are never touched; their diff against the starter is printed.

## Changes
- `internal/initcmd/plan.go`: pure `plan(existing, files, lock) (Result, error)`: absent → add; identical → unchanged; hash equals lock entry → update; otherwise (including no lock/no entry) → edited with a line diff; context docs create-if-absent only; lock entries missing from the starter → `Dropped`, never deleted.
- `internal/initcmd/lock.go` (new): `LockPath`, tolerant `ParseLock` (bad YAML or bad entries are ignored with warnings, so those files count as edited), deterministic `Marshal`, `withLock`.
- `internal/initcmd/initcmd.go`, `body.go`: read the lock from main, write/update it with every created or updated file, print edited/dropped files, PR body lists them.
- `internal/cli`: `--force` and `InitOpts.Force` removed; `InitOpts.Version` carries the ard version.
- `internal/committer/init.go`: `CheckInitPath` accepts `.ard/ard-init.lock`.
- `internal/roles/config.yaml`: starter `merge.protected_paths` gains the lock.
- Docs: getting-started, configuration, operations.

## Testing
`go test ./...` passes. New `plan_test.go`: table test for every planner case, context docs, dropped files, hand-edited lock, marshal round trip, `withLock`. Harness test for first run, rerun (nothing to propose), edited/stale/dropped. Extended `CheckInitPath` and protected-paths tests; removed the force tests.

## Open issues
- `--force` is removed (breaking for scripts), as the plan says.
- This repository's own `.ard/config.yaml` is not changed; a maintainer must add the lock to its `merge.protected_paths` by hand.
- Unchanged files get no lock entry, so a repo whose files already match the starter has no entries until a file is created or updated.
