# Plan for #340

## Implementation

Adopts the design on the issue (revision 3) with the refinements below, grounded in the current tree. The dependencies are on main: `internal/executor/workspace` (`Prepare`, `Discard`, `Remove`, `Prune`), `internal/tarball` (`Unpack`, `Limits`), `internal/github` (`ScopedToken`), `internal/executor/kube/api` (`Client`, types, `fake/`) and `internal/jobpod` (contract constants, `Encode`, `NewKey`, `SecretData`, `KeyMatches`). `internal/executor/kube` has no code yet except `api/`.

Refinements to the design as written:
- Use the real jobpod names: label keys are `jobpod.LabelRepo`, `LabelNetwork`, `LabelJob`, `LabelIssue`, `LabelStage`, `LabelRole`, `LabelPartOf`, `LabelComponent`, `LabelInstance` (prefix `ard.albertcbrown/`), not `ard/...`. Secret items are `jobpod.ItemManifest`, `ItemGitAccess`, `ItemResultKey` plus the model env name; build the Secret data with `jobpod.SecretData`. Use `jobpod.LabelValue` for role names.
- The `.done` marker in the work dir is the controller's file `<id>.done`; `jobpod.DoneName` is the separate pod-side marker in `/out`. Keep the names distinct in code.
- Confirm the flag names of `ard job-pre`, `job-exec`, `job-post` in `internal/cli/cmd_job.go` (`--deadline`, `--git-base`) before writing `buildPod`; do not edit the CLI.
- Network label: the per-role network field does not exist yet. Set `LabelNetwork` from `h.Network` only if the existing `executor.Network` value maps cleanly; otherwise omit and leave a TODO naming the follow-up.

### Steps

1. `internal/executor/executor.go`: add `Job.RoleSource struct{SHA, Path string}` (new), `Status.End` with constants `EndEvicted`, `EndCloneFailed`, `EndPrepFailed`, `EndNoResult`, `EndBadResult` (new; a small string type). `internal/executor/errors.go`: add `ErrInputsTooLarge`. Check `internal/executor/validate.go` and `internal/executor/local` still compile and that a zero `End` changes nothing.
2. `internal/executor/kube/kube.go` (new): `Tokens` interface, `Options`, `Executor` struct with `live map[string]*liveJob` guarded by a mutex, `New(Options) (*Executor, error)` validating required options and defaults (`PreBudget` 5m, `ResultGrace` 2m, `GitBase` https://github.com).
3. `kube.go`: `Prepare(ctx, j) (Handle, error)`: checks `len(ID) <= jobpod.MaxJobID`, `RoleSource` set; `WS.Prepare`; sets `Kind "k8s"`, `Container`, `Image`, `View = jobpod.AgentPaths()`; `jobpod.Encode`; on `jobpod.ErrTooLarge` call `WS.Discard` and return `ErrInputsTooLarge`.
4. `pod.go` (new, pure): `buildPod(h, c, o) *api.Pod` and `buildSecret(...)` per the ops notes: security contexts, volumes and size limits, mounts, resources, deadlines, labels, env (credential only as `secretKeyRef`). Unit-test before wiring.
5. `status.go` (new, pure): `mapStatus(h, pod, podErr, done, held, now)` implementing the issue's table; `LastOutput` from `h.LogPath` mtime.
6. `result.go` (new): `swap` (rename `<id>` to `<id>.input`, `<id>.result/work` to `<id>`, then `<id>.done` with `O_EXCL` 0600 and fsync of the dir); each step re-checkable after a crash.
7. `watch.go` (new): per-job goroutine polling `GetPod` every 2s; revoke token once when `pre` terminates; map pre failures, pull back-off, unschedulable for 5m, eviction, `DeadlineExceeded` to `.done` with `End`; after `agent` terminates, pull the result from the pod IP with the bearer key, retry 409/429/5xx/network until `Deadline+ResultGrace`, a fresh `<id>.result` per attempt, `tarball.Unpack`; `EndBadResult` with no retry; verify pod UID unchanged before pulling; copy `out/stdout` to `h.StdoutPath`. Use `HTTP` and `Now` options for tests. Sanitise and cap termination messages.
8. `kube.go`: `Start` (refuse `Stdin`, `Policy` checks, config-dir Fixed must equal `jobpod.AgentConfig`, `ErrNotPrepared`, set `Deadline`, mint token for job and sidecar repos, `jobpod.NewKey`, `CreatePod` then owner-referenced `CreateSecret`, rollback with pod delete and revoke on failure, start watcher and log follower), `Status`, `Logs` (read local files as `internal/executor/local/local.go` does), `Cleanup` (idempotent; foreign Kind removes dirs only; the moved worktree is removed by directory removal then `Prune`, not `worktree remove`).
9. `logs.go` (new): follow `PodLogs` for `agent` into `h.LogPath`, strip timestamps, reconnect from the last timestamp until the agent ends.
10. `kube.go` or `sweep.go` (new): `Sweep(ctx, keep func(id string) bool)` selecting on repo and component labels; `Secrets() []string`; Reaper kill path goes through `Cleanup`.
11. `docs/layout.md`: replace the `internal/executor/kube/` mention so it lists the executor files; keep the `api/` line.
12. Run `make lint test`. If the diff passes about 1,500 lines, split steps 9 and 10 into a follow-up as the issue says.

### Test plan

Command: `make lint test` (or `go test ./internal/executor/...`). All tests use `api/fake` and `httptest`; no network or cluster.
- `TestBuildPod` (`pod_test.go`), table: plain, ReadOnly, sidecar, history, API key vs OAuth. Expect: agent has no Secret volume and no token or key in env; only `pre` mounts the token item and only `post` the result key; security contexts match the ops notes; deadlines; name <= 63; labels valid label values.
- `TestMapStatus` (`status_test.go`): one case per row of the table, plus foreign Kind and missing worktree.
- `TestLifecycle` (`kube_test.go`): Prepare, Start, pre terminated (token revoked once), agent exit 3, pull, `Exited` with code 3, worktree swapped, stdout copied.
- `TestPull` (`watch_test.go`), table: 409 twice then 200; unsafe tar gives `EndBadResult` with no retry; over limit; deadline gives `EndNoResult`; pod UID changed is not pulled.
- `TestEnds`: eviction, image pull back-off, unschedulable timeout, pre exit 3 and 1, `DeadlineExceeded` gives 124 and `TimedOut`.
- `TestStartRollback` (Secret create fails: pod deleted, token revoked), `TestInputsTooLarge`, `TestStartRefusals` (Stdin, bad config-dir, not prepared, ID too long, no RoleSource), `TestOrphanAfterRestart`, `TestCleanupIdempotent` (including KeepWorktree and foreign Kind), `TestSweep` (another repo's pods untouched), `TestLogFollowReconnect`, `TestSwapCrashResume`.
- Run with `-race`.

### Risk

High. A state machine across the controller, kubelet and pod, with credentials (read token, result key, model credential) and HTTP pull of untrusted tar content. Mitigated by pure `buildPod` and `mapStatus`, the unpack limits in `internal/tarball`, and the fake. No protected path is touched (`docs/layout.md` and `internal/` are not in `merge.protected_paths`); no `deploy/` change. The change widens the `executor` public types (`Status.End`, `Job.RoleSource`); `local` must keep behaving identically. Concurrency: one watcher goroutine per job and a shared `live` map; needs `-race`.

### Size

L, about 1,300 lines (roughly 700 code, 600 tests), within the 3,000-line worker block limit. Files: `internal/executor/executor.go`, `internal/executor/errors.go`, `internal/executor/kube/{kube,pod,status,result,watch,logs}.go` and their `_test.go` files (all new), `docs/layout.md`.
