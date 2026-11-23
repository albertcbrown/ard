---
role: engineer
status: done
attempt: 1
---
`ard serve` and `ard config check` now refuse to run on a git older than 2.47.0, with an error naming the version found and the minimum. This stops operators with an old git from hitting the committer's fetch failures mid-job.

## Changes
- `internal/gitx/version.go` (new): `Version` type, `MinVersion` (2.47.0), `ParseVersion` (tolerates a missing patch and vendor suffixes), `Git.FullVersion` and `Git.CheckVersion`. The existing `Git.Version` (major, minor) is unchanged.
- `internal/servecmd/serve.go`: runs the check right after the repo is resolved, before any state, lock or clone work. Exit code 1.
- `internal/configcmd/config.go`: runs the check first in `Check`. A package var `gitBin` lets tests inject a shim.
- `docs/proposal.md`: one sentence stating the minimum.
- Tests: `internal/gitx/version_test.go` (parse, ordering, shim-based check), plus an old-git refusal test in servecmd and configcmd.

## Testing
Ran `gofmt -l`, `go vet ./...` and `go test ./...`; all pass. Local git is 2.56.1.

## Open issues
- The minimum of 2.47.0 is a conservative guess: only 2.47.3 (passes) and 2.39.5 (fails) are known. A maintainer can lower `MinVersion` after running the committer and gitx tests on intermediate versions.
- Making the fetch work on old git is left as a follow-up. README has no setup section, so it is unchanged.
