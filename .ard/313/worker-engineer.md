---
role: engineer
status: done
attempt: 1
---
Moves the filesystem half of the local executor into a new `internal/executor/workspace` package with no behaviour change, so a later kube executor and the in-pod job code can reuse it. The env policy moves into package `executor`. `CopyRole` is not added (design revision 3 drops it), and `GOFLAGS=-buildvcs=false` for Docker was already in `runArgv` and is documented in proposal.md.

## Changes
- `workspace/`: `Workspace` with `New`, `Prepare`, `Discard`, `Remove`, `Prune`; layout helpers (`WorktreePath`, `HomePath`, `LogDir`, `LogBase`); `Populate`, `MakeHome`, `ConfigDirName`, `scrub`, `rejectSymlink`; exported `ChmodReadOnly`, `ChmodWritable`, `RemoveAllWritable`; the sidecar cache and locks (moved from local) with exported `SidecarGit`, `SidecarCache` and the new `FetchPinned`.
- `executor/envpolicy.go`: `EnvPolicy{AllowEnv, AllowFixed}` with `CheckEnv`/`CheckFixed`, `ArdEnv`, `IsGitHubEnvName`.
- `local`: builds a `workspace.Workspace`; `Prepare` calls it and keeps the Docker/ReadOnly tail, calling `Discard` if the tail fails; `Cleanup` keeps stop and `docker rm`, then `ws.Remove`; `Prune` delegates. `Options`, `LogBase` and `WorktreePath` keep their signatures. `execGit` stays in local.
- `docs/layout.md`: new workspace line, updated local line.

## Testing
`go test ./internal/executor/...` and `go vet` pass; `gofmt` is clean. Existing local tests are unchanged apart from mechanical edits: they reach the fake git, the clock and the layout helpers through `Options` and `l.ws`, and `TestForbiddenEnv`/`TestFixedEnv` became `TestEnvPolicy`. Moved: `TestRemoveAllWritable_*`, `TestLayout` and `TestScrubGuards` into `workspace`. Added: `TestPopulate`, `TestFetchPinned`, `TestDiscard`, `TestPrepareRollback`, a foreign-repo `Remove` test, `TestEnvPolicy`, `TestArdEnv`. `go build ./internal/servecmd ./internal/configcmd ./internal/committer` succeeds with the offline module cache; a full `go test ./...` could not run here because modules need downloading.

## Open issues
- `FetchPinned` fetches a SHA into `refs/ard/pin`, where local used `refs/ard/sidecar/<sha>`. The ref name is internal and no test depends on it.
- `Prepare` now registers home for rollback before `Populate` runs, so a failure while making the home also removes it.
