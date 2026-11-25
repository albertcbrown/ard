---
role: engineer
status: done
attempt: 1
---
Wires the Kubernetes executor into `ard serve` (design revision 3, no listener): `executor:` picks local or k8s at startup, cluster settings are `--k8s-*` flags with `ARD_K8S_*` env fallbacks, and the Reaper, close-out and job builders handle the k8s end states.

## Changes
- `internal/cli`: `K8sOpts` and the eight `--k8s-*` flags with env fallbacks; an image without `@sha256:` is refused (exit 2).
- `internal/servecmd`: `jobExecutor` type; `newExecutor` switches on the config's executor (fixed after first build, `errModeUnknown`, PAT refused via `ScopedMinter`, missing flags named); config-drift check pauses serve with "restart ard serve"; startup exit 1 when a k8s executor cannot be built; orphan `Sweep` in the `pool-adopt` hook; `w.environ()` adds live tokens to the redaction set (reaper, worker, reviewer).
- `internal/executor`: `kube.Executor.Prune`, `local.ExecGit` (shared worktree runner), `InputsTooLargeDetail`.
- `internal/closeout`, `internal/reaper`: `CauseEvicted`, `CauseInputsTooLarge`; `CauseOf` maps `End` values.
- `internal/agentjob`, `worker`, `scanner`: `RoleSource{SHA, Path}` on jobs. `worker`, `triage`, `reviewer`: `ErrInputsTooLarge` closes out as `inputs_too_large`, no retry.
- `internal/config`: `roles_repo` with `executor: k8s` is a config error. `configcmd`: a stderr note for k8s.
- Docs: `docs/proposal.md` (serve wiring paragraph, not-an-effect amendment), `docs/layout.md`.

## Testing
`go test ./...` passes, local e2e tests unchanged. Added: `TestNewExecutorMode`, `TestExecutorDrift`, `TestLateK8sConfigWithPATPauses`, `TestK8sRefusesPAT` (live and dry-run), `TestSweepOrphans`, `TestParseResources`, `TestEnvironIncludesTokens`, `TestServeK8sFlags`, kube `TestPruneDelegatesToWorkspace`, and table rows for `CauseOf` ends, `roles_repo` under k8s, `RoleSource` and `inputs_too_large` in worker, triage and reviewer.

## Open issues
- With no config loaded and no `--k8s-*` flag set, the mode defaults to local (so a serve that starts paused on a bad config still builds its loops, as today). With cluster flags set it is `errModeUnknown`. A later k8s config then needs a restart. This departs slightly from the plan to keep local behaviour unchanged.
- The scanner engine sets `RoleSource` but has no `inputs_too_large` handling: scanner jobs carry no untrusted inputs and have no cause path.
- `--k8s-resources` keys (`cpu`, `memory`, `memory-limit`, `ephemeral-storage`) apply to the agent container only; this mapping was my choice.
- The issue says S-M but the change is L; a maintainer may want to confirm.
