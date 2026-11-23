---
role: engineer
status: done
attempt: 1
---
Adds a built-in `scanners/trufflehog-history` scanner that scans the full git history for leaked credentials, and an executor change that gives a scanner job a read-only view of the controller's bare clone. Fingerprints use the same salt as the HEAD scanner, so one value has one fingerprint in both.

## Changes
- Executor: `Job.History` (scanner stage only), `Paths.History`, `Handle.HistoryDir`. Docker mounts the clone at `/history:ro` with a fixed `safe.directory` git setting. Host mode uses the clone path. `ARD_HISTORY` is set in both.
- Config: `Role.ReadsHistory()` is true for a deterministic scanner whose directory holds an empty file named `history`. There is no config key, so an override cannot grant it. The scanner engine sets `Job.History` from it.
- New role `internal/roles/scanners/trufflehog-history/`: Dockerfile (same pins), `scan` (`trufflehog git`, no partial-scan flags, fail-closed, same hash line), `findings.jq` (reads git location, keeps only detector, file and line, with a no-autoclose runbook), `exclude-paths`, `history` marker.
- Starter: embedded in `roles.go`, override in `config.yaml` (7d, autoclose false, needs-human, 60m, max_open 5), PR-body note in `initcmd/body.go`. `starter()` now sorts by full path, because `trufflehog/` would otherwise precede `trufflehog-history/`.
- Docs: `proposal.md`, `layout.md`, `TODO.md`.
- Tests: validation, `Prepare`, docker argv, `ReadsHistory`, engine job, embedded role, and `trufflehog_history_test.go` (fingerprint parity, no leaks, fail-closed, argv, missing tool or `ARD_HISTORY`, zero chunks with commits, Dockerfile and salt parity).

## Testing
`go test ./...` passes. I did not run `make lint`. `gofmt -l` flags only the existing `internal/roles/testdata/docs/skips/src/_x.go`, a deliberately broken fixture.

## Open issues
- The Docker integration test from the plan (git log in a container, write to `/history` fails) was not added. It is untested against real Docker, including whether `safe.directory` is needed.
- The read-only clone mount exposes every fetched ref, including unmerged PR branches. A maintainer should confirm the mount over a bundle.
- Whether a value found by both scanners becomes one issue depends on the scanner engine's cross-role handling, which this change does not touch.
- Enabling the scanner in this repository needs a maintainer-made copy under `.ard/scanners/` and an entry in `.ard/config.yaml`.
- No Kubernetes executor exists yet; only a comment on `Paths.History` and `proposal.md` document the contract.
