---
role: engineer
status: done
attempt: 1
---
Adds the Kubernetes executor in `internal/executor/kube`: it builds the job pod and Secret, watches the pod, pulls the result archive back from the post container, maps pod state to executor status, follows logs, and cleans up and sweeps. The shared types it needs are added to `internal/executor`.

## Changes
- `internal/executor`: `Job.RoleSource`, `Status.End` with the five `End*` constants, `ErrInputsTooLarge`.
- `kube/kube.go`: `Tokens`, `Options`, `New`, Prepare, Start (with rollback), Status, Logs, idempotent Cleanup, `Sweep`, `Secrets`.
- `kube/pod.go`: pure `buildPod` and `buildSecret`. Pre and post get separate Secret volumes, so only pre mounts the token and only post mounts the result key. The agent gets the credential only as a `secretKeyRef`.
- `kube/status.go`: pure `mapStatus`. `kube/result.go`: crash-resumable `swap` and the `.done` marker. `kube/watch.go`: poller, result pull, end mapping. `kube/logs.go`: log follower.
- `kube/api`: `FSGroupChangePolicy` field; the fake gains `SetInitContainer` and `Mutate`.
- `docs/layout.md`: the new package line.

## Testing
`go vet ./internal/executor/...` and `go test -race ./internal/executor/...` pass, including `-count=5` on the kube package. gofmt is clean on the changed files. Tests use `api/fake` and an httptest server only: TestBuildPod, TestBuildSecret, TestMapStatus, TestLifecycle, TestPull, TestEnds, TestStartRollback, TestInputsTooLarge, TestPrepareRefusals, TestStartRefusals, TestOrphanAfterRestart, TestCleanupIdempotent, TestSweep, TestLogFollowReconnect, TestSwapCrashResume.

## Open issues
- `Sweep(ctx, keep)` takes the repo from a new `Options.Repo`, because the plan gave it no repo argument.
- Network label: `NetworkAdvisory` and `NetworkNone` both map to `false`; `NetworkOn` maps to `true`.
- After the swap, the original worktree's `.git` link is moved into the result so close-out's git calls still work. `tarball.Unpack` already refuses a `.git` entry.
- `Status` reports a held job whose pod vanished as Running until the watcher writes the marker, so Gone carries the real End reason and not `EndNoResult`. `mapStatus` itself follows the table.
- The pod has no scheduling condition in the API types, so "unschedulable" is approximated as Pending with `pre` still waiting for 5 minutes.
- `golangci-lint` reports errcheck findings in the repo already; the repo's own `make lint` (vet and gofmt) is clean for these files.
