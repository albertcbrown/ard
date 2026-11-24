---
role: engineer
status: done
attempt: 1
---
Enforces a worker role's `network: false` for the test step in Docker by passing `--network none`. Agent steps stay advisory because they need the model API.

## Changes
- `internal/executor/local/docker.go`: `runArgv` adds `--network none` for `NetworkNone`; advisory and the zero value add nothing.
- `internal/worker/run.go`: `execJob` marks Jobs `NetworkAdvisory` when the role has `network: false`. The test step in `testLoop` is then set to `NetworkNone`.
- `internal/config/types.go`: comment on `Role.Network` updated. The rendered config strings are unchanged, since agent steps are still advisory.
- Tests: `runArgv` table cases in `misc_test.go`; new `TestNetworkPerStep` in `loop_test.go`.

## Testing
Ran `go test ./internal/executor/... ./internal/worker/... ./internal/config/... ./internal/configcmd/...`; all pass.
`TestNetworkPerStep` covers a test-fix loop (t1, fix, t2). With `network: false` both test steps carry `NetworkNone` and the other steps carry `NetworkAdvisory`. With `network: true` all Jobs carry `""`.

## Open issues
- A role whose `tests` command needs the network (for example a dependency download) will fail in Docker once `network: false` is set. This is intended.
- Host mode and k8s do not enforce it; the host warning from the earlier part still applies.
