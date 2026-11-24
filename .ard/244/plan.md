# Plan for #244

## Implementation

### Steps
1. `internal/executor/validate.go` and `errors.go`: add `ErrImageRef` (new sentinel, next to `ErrInvalidJob`) and `func CheckImageRef(ref string) error`. Accept only `<name>@sha256:<64 hex>` or a bare `sha256:<hex>` local image ID (hex 12–64). Reject empty, leading `-`, whitespace, control characters and tags such as `ard-agent:dev`, wrapping `ErrImageRef`. Reason: the value goes straight into `docker run` argv.
2. `internal/executor/local/local.go`: add `Options.AgentImage string` and an unexported `Local.agentImage` field; `New` calls `executor.CheckImageRef` when non-empty and returns the error. Empty means today's behaviour.
3. `internal/executor/local/docker.go`: add new `func (l *Local) ensurePulled(ctx, ref, logPath string) error`. It runs `docker image inspect ref` and returns nil if present (a local `sha256:<id>` image is never pulled). Otherwise, for a digest ref, it runs `docker pull ref` with `l.buildTimeout`, appends the output to the log, and fails on error. Reuse `l.docker`.
4. `internal/executor/local/local.go` `Prepare`: after the sidecar and log setup, branch as follows.
   - `j.Dockerfile != ""`: unchanged; the role's own Dockerfile wins.
   - else if `l.agentImage != "" && !j.TrustedInput`: call `ensurePulled`, then take the same Docker branch (`Kind="docker"`, `Container="ard-"+ID`, `Image=l.agentImage`, `View` set to `/work`, `/role`, `/ard-config`, `/history`, `/sidecar`). Factor the shared Docker setup into a helper so the flags and mounts are identical.
   - On a Docker failure, return the error through the existing cleanup `defer`. Never fall back to `Kind="host"`.
   - Otherwise (unset, or a trusted-input deterministic scanner) behave as today.
5. `internal/executor/local/docker.go` `runArgv` and `Handle`: mount the controller clone read-only at its host path when the job runs in the default image, so `git diff` and `log` work, because the worktree's `.git` file points at `<RepoDir>/worktrees/...`. Add `-v h.RepoDir:h.RepoDir:ro`. Apply it only to the default-image path, or to every Docker job if the existing Dockerfile roles already need it. Add `-e GIT_CONFIG_COUNT/KEY/VALUE` for `safe.directory` on that path, following the `HistoryDir` pattern. If `History` is already mounted, do not mount `RepoDir` twice. Check `Handle` JSON compatibility: add a field only if needed, and keep it optional.
6. `internal/executor/local/hostwarn.go`: add `Local.AgentImage`-aware filtering. Export a way for the callers to know which roles are now covered, for example `HostReach()` unchanged plus a new text variant. `warnHostJob` and `hostUntrusted` already key on `Kind == "host"`, so covered jobs stop warning without a change. Update `hostHint` to mention `ARD_AGENT_IMAGE`.
7. `internal/config/types.go`: change `HostUntrusted()` to take no argument as now and add `HostUntrustedWith(agentImage bool)`, or have the callers skip all roles when an image is set. Keep `HostUntrusted()` as is for compatibility.
8. `internal/servecmd/serve.go` and `cfgloader.go`: read `ARD_AGENT_IMAGE` with `os.Getenv`, run `executor.CheckImageRef`, and fail startup before `serve.start` on an invalid value. Pass it as `local.Options.AgentImage` in `newExecutor` (around line 411). When it is set, run `docker version` (or `docker image inspect`) at startup and fail if Docker is down. `cfgLoader.warnHost` skips roles covered by the image. Log `serve.agent_image` at start.
9. `internal/configcmd/config.go`: read `ARD_AGENT_IMAGE`, validate it, and report an invalid ref as an error. Skip the host warning for covered roles.
10. `docs/proposal.md`: near line 285 (local-executor details) and line 526 (agent image), document `ARD_AGENT_IMAGE`, the precedence (a role's Dockerfile wins), the no-fallback rule, the read-only clone mount, and the changed `executor.host_untrusted` warning.

### Test plan
Command: `make lint test`.
- `internal/executor/validate_test.go` `TestCheckImageRef`: accepts `x/y@sha256:<64hex>` and `sha256:<12hex>`; rejects "", `ard-agent:dev`, `-x`, `a b`, short or non-hex digests. Each rejection satisfies `errors.Is(err, ErrImageRef)`.
- `local_test.go` (fake docker binary, as the existing tests do):
  - `TestPrepareDefaultImage`: untrusted job, no Dockerfile, `AgentImage` set gives `Kind=="docker"`, `Image==ref` and `Container=="ard-<id>"`.
  - `TestPrepareDockerfileWins`: a role with a Dockerfile uses its own tag.
  - `TestPrepareTrustedScannerStaysHost`: `TrustedInput` gives host.
  - `TestPrepareUnsetUnchanged`: no `AgentImage` gives host.
  - `TestPrepareDockerDownNoFallback`: the fake docker fails, Prepare returns an error, no worktree is left behind and no host handle is returned.
  - `TestEnsurePulledLocalImage`: inspect succeeds, no pull call.
  - `TestEnsurePulledPullsDigest`: inspect fails, a pull happens; a pull failure is an error.
  - `TestNewRejectsBadAgentImage`.
- Argv test: the default image path has the same flags as a Dockerfile role (`--cap-drop ALL`, `no-new-privileges`, `/work`, `/role:ro`), plus the `RepoDir:RepoDir:ro` mount. No secret values appear.
- `hostwarn_test.go`: a Docker-kind job is not warned.
- `servecmd` and `configcmd` tests: an invalid `ARD_AGENT_IMAGE` fails startup or check; a valid one suppresses the host warning for covered roles; unset keeps the warning.
- Edge cases: a Dockerfile role with the env set; a read-only job; a job with a sidecar and history.
- Human only: the Docker integration test (`docker_integration_test.go`) and the live Mac check (`docker ps` shows `ard-<job>`, no `executor.host_untrusted` row).

### Risk
High. It decides whether untrusted-input agents run in a container or on the host, so the no-fallback rule needs close review. It adds an env setting (a public interface). It changes the mounts for the controller clone (read-only, security-sensitive). No protected paths from merge.protected_paths (.github/, .ard/config.yaml, .ard/scanners/, .ard/triage/, .ard/workers/, .ard/reviewers/) are touched. There are no migrations. Concurrency is unchanged, except that concurrent pulls of one image are possible, and Docker serialises them.

### Size
L. Files: `internal/executor/errors.go`, `validate.go`, `validate_test.go`; `internal/executor/local/local.go`, `docker.go`, `hostwarn.go` and their tests; `internal/config/types.go` (if step 7 is used); `internal/servecmd/serve.go`, `cfgloader.go` and tests; `internal/configcmd/config.go` and tests; `docs/proposal.md`.
