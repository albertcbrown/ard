---
role: engineer
status: done
attempt: 1
---
Adds a `scanners/trufflehog-history` scanner that scans the full git history through a read-only mount of the controller's bare clone, so credentials removed from HEAD are still reported. Fingerprints match the HEAD scanner's, and no secret, fragment or matched line leaves the scan.

## Changes
- Executor: `Job.History` (scanner stage only), `Paths.History`, `Handle.HistoryDir`; Docker mounts the clone at `/history:ro` and sets `ARD_HISTORY`, with `safe.directory` for that path. Host mode passes the clone path directly and does not enforce read-only (now stated in the comments).
- Config/engine: a role asks for history with an empty `history` marker file (`Role.ReadsHistory`); no config key.
- New role `scanners/trufflehog-history` (Dockerfile, scan, findings.jq, exclude-paths, marker), embedded and added to the default config (7d, autoclose false, route needs-human, 60m). It never autocloses.
- Docs: `docs/proposal.md`, layout and TODO notes; init body sentence.
- Tests for validation, Prepare, argv/env, `ReadsHistory`, the engine and the scanner script (fake binaries).

## Review findings
- `local_test.go` (Docker integration test missing): fixed. `TestDockerHistory` in `docker_integration_test.go` (integration tag, skipped without Docker) runs `git --git-dir /history log --all` in a container, checks more than one commit, and checks a write to `/history` fails. It passes here, so `safe.directory` works.
- `local.go` (host mode comment): fixed. `Paths.History`, `Job.History` and `Prepare` say host mode does not enforce read-only.

## Other fixes from the owner's security review
- The scan now passes `--bare` to `trufflehog git`; `TestTrufflehogHistoryArgv` updated, docs updated.
- Added `TestTrufflehogHistoryRealBinary`, which runs real trufflehog against a bare repo with refs only under `refs/ard/*` (key values built at run time). It expects one finding, no secret on stdout or stderr and a clean TMPDIR. It skips without trufflehog.
- Issue text now says the value may be only on an unmerged branch ard fetched.

## Testing
Ran `go test` for roles, executor, executor/local, config, scanner and initcmd: all pass. Ran `go test -tags integration -run TestDockerHistory ./internal/executor/local` against local Docker: pass. Real trufflehog 3.100.0 test passes. `gofmt -l` reports only the deliberately broken `internal/roles/testdata/docs/skips/src/_x.go` fixture.

## Open issues
- No separate killed-mid-stream test was added; the truncated-output cases (non-zero exit, missing summary) are covered by the fail-closed test.
- Mount versus bundle remains a design choice for a maintainer to confirm.
