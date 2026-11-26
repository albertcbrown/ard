---
role: engineer
status: done
attempt: 1
---
The Kubernetes executor now builds a role's Dockerfile in the cluster and runs the job on the resulting image, pinned by digest, instead of ignoring the Dockerfile. The image tag is a content hash of the role directory at the pinned role SHA, so an unchanged role is built once.

## Changes
- `internal/executor/kube/roleimage.go` (new): `RoleImages` options, `RoleTree` (git `ls-tree`/`archive` in the controller clone at the pinned SHA, never the worktree), tag `h-<hash>`, registry check (401/403/404 mean not built, other errors fail as a registry check), single-flight per tag, builder pod in the shape the #448 admission policy admits, wait within a budget, typed `ErrRoleImageBuild` causes (exit code and redacted log tail, timeout, refused pod, no digest), and cleanup of pod and Secret.
- `kube.go`, `pod.go`: `Prepare` builds before the worktree exists and sets the image; with `RoleImages` nil it still logs `kube.dockerfile_ignored`. The agent container uses the handle's image; the pre and post containers keep the pod image.
- `api/types.go`: AppArmor profile field.
- `internal/cli`, `internal/servecmd/serve.go`: `--k8s-build-namespace`, `--k8s-builder-image`, `--k8s-builder-uid`, `--k8s-role-image-repository`, `--k8s-role-image-push-secret` (and the `ARD_K8S_*` env the chart sets). Images are on only when the build namespace is set; partial settings or an undigested builder image are errors.
- `internal/executor/local/docker.go`: `--build-arg AGENT_IMAGE=<agent image>` when one is configured.
- `docs/proposal.md`, `docs/runbooks/role-images.md`: the k8s behaviour, and the `:latest` base residual risk (already recorded, now tied to the build).
- Tests: `roleimage_test.go` (tag stability, cached, success and pod shape, SHA-only context, private package, registry down, failure with redaction, timeout, admission denied, bad digest, single-flight, rejects, real-git check), `k8s_test.go`, `agentimage_test.go`.

## Testing
`make lint` passes. `go test ./...` passes (including `-race` on the kube package).

## Open issues
- The builder command (`buildctl-daemonless.sh`, `/dev/termination-log` metadata file, `DOCKER_CONFIG` mount, `--oci-worker-no-process-sandbox`) has not been run against a real cluster or the pinned BuildKit image; only the fake client covers it.
- The plan says a Secret first, then the pod. I create the pod first, then the Secret owned by it (as `buildSecret` does for job pods), so a refused pod leaves no Secret.
- The role path must be `.ard/<scanners|triage|workers|reviewers>/<name>`; other paths are rejected for builds.
- No `deploy/` change was needed.
