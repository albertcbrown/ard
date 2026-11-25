# Plan for #318

## Implementation

Wire the kube executor (landed in `internal/executor/kube`) into `ard serve`, from design revision 3 (no listener). The earlier job-API plan is obsolete. Checked against the code as it landed; corrections to the comment plan are marked **(corrected)**.

### Steps

1. **`internal/executor/kube/kube.go`** (new method, **corrected**): `func (e *Executor) Prune(ctx context.Context, olderThan time.Duration) error { return e.o.WS.Prune(ctx, olderThan) }`, as `local.Local.Prune` does (local.go:229). `reaper.Executor` (reaper.go:44) requires `Prune` and the kube executor has none, so serve wiring would not compile without it. Add a one-case test in `kube_test.go`.
2. **`internal/cli`**: `ServeOpts` (app.go:42) gains `K8s K8sOpts{Namespace, Instance, AgentImage, PodImage, GitBase, Resources string; PreBudget, ResultGrace time.Duration}`. `cmd_serve.go` adds `--k8s-namespace`, `--k8s-instance`, `--k8s-agent-image`, `--k8s-pod-image`, `--k8s-git-base` (default `https://github.com`), `--k8s-resources`, `--k8s-pre-budget` (5m) and `--k8s-result-grace` (2m). Each has an `ARD_K8S_*` env fallback read in cli. Images with no `@sha256:` are refused. Parse the resources string into `kube.Resources` in servecmd (check the fields in kube/pod.go, `DefaultResources`).
3. **`deps`** (serve.go, `buildDeps` ~line 546): add `Auth github.Auth`, set from the `auth` already built there.
4. **Executor type** (**corrected**): `wiring.exec` (serve.go:389) becomes `jobExecutor` = `interface{ executor.Executor; Prune(ctx, time.Duration) error }`. `newExecutor` returns it. Check that the loop builders (scanners.go, triage.go, worker.go, reviewer.go, reaping.go) take interfaces. Widen nothing.
5. **`newExecutor`** (serve.go:441): switch on `w.cfg.Load().Executor`, read at the first successful build and then fixed in `w.mode`.
   - No config yet: return `errModeUnknown` (new), do not cache it, and log `serve.executor_unresolved` once.
   - `local`: as today.
   - `k8s`: `github.ScopedMinter(d.Auth)`; `ErrCannotScope` is an error, also under `--dry-run`. `workspace.New` with the options `local.New` gets. `newKubeClient` is a new package var, default `func() (api.Client, error) { return api.InCluster(api.RESTOptions{}) }`, so tests inject `api/fake`. Then `kube.New(kube.Options{Client, Tokens: minter, WS, Namespace, Instance, Repo: w.Repo.String() (the owner/name form; check the github.Repo method), AgentImage, PodImage, GitBase, Resources, PreBudget, ResultGrace, Log})`. kube.New requires Namespace, Repo, Instance and both images, so a missing flag gives an error naming the flag, checked before calling it. Check `ConfigDirEnv` and `Policy` against how `local.New` is given them, and whether k8s needs them.
   - At startup `serveLoops` exits 1 on the PAT refusal. If the config only arrives later, the build error pauses serve with that message.
6. **Config drift**: in the `Config` callback (serve.go:~270), once `w.mode` is set, `c.Executor != w.mode` returns `executor changed from X to Y; restart ard serve`. The existing path counts it as paused and reports it on the status issue.
7. **`roles_repo` under k8s**: in `internal/config/defaults.go` (next to line 243), `executor: k8s` with `roles_repo` set is an `*Error{Key: "roles_repo"}` ("not supported with executor k8s yet"). It fails closed.
8. **Job builders**: set `RoleSource{SHA: cfg.SHA, Path: role.Dir}` in `agentjob.ExecJob` (command.go), `worker.execJob` (run.go) and the scanner engine (engine_exec.go). kube `Prepare` rejects a job without it (kube.go:137). Check the exact config SHA field name. Local ignores it.
9. **Prepare failures**: next to the `ErrClone` checks (worker/run.go, triage/run.go, reviewer/run.go and the scanner engine), `errors.Is(err, executor.ErrInputsTooLarge)` closes out with `CauseInputsTooLarge`, no retry, detail "untrusted inputs exceed 768 KiB". Confirm the limit text against `tarball.DefaultLimits()`.
10. **Causes**: `closeout.go:~106` adds `CauseEvicted = "evicted"` and `CauseInputsTooLarge = "inputs_too_large"`. Check any exhaustive switch or list of kinds, including docs and the closeout renderer. In `reaper.CauseOf` (finish.go), before the generic `Gone` case, switch on `st.End`:
    - `EndEvicted`: `CauseEvicted`, retry.
    - `EndCloneFailed`: `CauseCloneFail`, retry.
    - `EndPrepFailed`, `EndBadResult`, `EndNoResult`: `CauseNoProcess` with `st.Reason` (confirm the field in `executor.Status`), no retry.
    - A `Gone` with no `End` is unchanged.
11. **Orphans**: in the `pool-adopt` hook, when the executor implements `interface{ Sweep(context.Context, func(string) bool) error }`, call it before `adoptPool`. `keep` accepts the ids of `store.RunningJobs` (and step ids if the pod label uses them; check `jobpod.LabelJob`). Pods with a record go through the existing `Orphaned` → `actFinishOrphan` → `Cleanup` path. Update the comment at reaper.go:43 to "(*local.Local or *kube.Executor)".
12. **Redaction**: add `w.environ()` = `os.Environ()` plus `ARD_K8S_TOKEN_<n>=<v>` for each value of the executor's `Secrets()` (kube.go:389). Use it in place of `os.Environ` at reaping.go:69, worker.go:50 and reviewer.go:38 (**corrected**: the reviewer also reads the environment). It is a no-op for local, which has no `Secrets()`.
13. **`configcmd`**: with `executor: k8s`, print one line "cluster settings come from `ard serve` flags". No new config keys.
14. **Docs**: `docs/proposal.md` (Executors table, a k8s details paragraph, the "executor is not an effect" amendment, the Reaper paragraph on the sweep and `evicted`); `docs/layout.md` servecmd line. The docs drift test is part of `make test`.

### Test plan

Command: `go test ./...` (`make test`). No network: `api/fake`, the GitHub fake, `t.TempDir()`.
- `internal/servecmd/serve_test.go` `TestNewExecutorMode` (table):
  - local gives a `*local.Local`;
  - k8s with the fake client gives a `*kube.Executor`;
  - no config gives `errModeUnknown` and a later call succeeds;
  - k8s without namespace names `--k8s-namespace`.
- `TestK8sRefusesPAT`: `TokenAuth` with k8s exits 1, also with `--dry-run`.
- `TestExecutorDrift`: the config flips to `local` and the gate reports paused with the restart message.
- `internal/config` `TestRolesRepoK8sRefused`.
- `internal/servecmd/adopt_test.go` `TestSweepOrphans`: pods for a running job, an unknown job and another repo label; only the unknown one is deleted. A local executor skips the sweep.
- `internal/reaper/finish_test.go` `TestCauseOfEnds` (one row per `End`, plus `Gone` with no `End` unchanged).
- Worker `TestInputsTooLargeCause`, with a fake executor returning `ErrInputsTooLarge`. Triage and reviewer get the same case.
- Builder tests: `RoleSource` is set in the worker, triage, reviewer and scanner jobs.
- `TestRedactionIncludesTokens`: a secret from a fake executor is masked in a close-out log.
- kube `TestPrune` delegates to the workspace.
- `internal/cli/cli_test.go`: flags parse, env fallbacks apply, and an image with only a tag is refused.
- Regression guard: the local e2e tests (`worker_e2e_test.go`, `triage_e2e_test.go`) pass unchanged.

### Risk

Medium. It changes the executor type every loop shares and adds startup and config refusals. Credentials are involved: scoped read tokens and the redaction set. No protected path (`.github/`, `.ard/config.yaml`, role directories) is touched. There are no migrations and no new listener. The unchanged local e2e tests guard against regressions.

### Size

L, lower end: about 400 lines of code and 350 of tests, under the 3,000-line block limit. Files: `internal/executor/kube/kube.go` and its test, `internal/cli/{app.go,cmd_serve.go}`, `internal/servecmd/{serve.go,reaping.go,worker.go,reviewer.go}`, `internal/config/defaults.go`, `internal/closeout/closeout.go`, `internal/reaper/{finish.go,reaper.go}`, `internal/agentjob/command.go`, `internal/worker/run.go`, `internal/triage/run.go`, `internal/reviewer/run.go`, the scanner `engine_exec.go`, `internal/configcmd`, `docs/proposal.md`, `docs/layout.md`, and tests.
