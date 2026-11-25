# Plan for #321

## Implementation

Adopts the design-revision-3 plan from the issue comments, with these refinements from reading the current tree: `deploy/kind-up.sh` does not exist and `deploy/` is a protected path, so the test creates the cluster itself; `internal/gitx/gittest`, `internal/github/fake` and `internal/executor/kube/api` already exist and are reused; the chart values to set are in `deploy/charts/ard-serve/values.yaml` (`executor.kind`, `instance.dryRun`, `apiServer`, `jobs.githubCIDRs`, `jobs.modelAPICIDRs`, `image.digest`, `agentImage.digest`, `storage.accessMode`). The worker must not edit `deploy/`, `Dockerfile` or `.github/`.

### Steps
1. New `internal/executor/kube/e2e/kind_integration_test.go` (`//go:build integration`, package `e2e`). `TestKind*` entry points call `t.Skip` unless `ARD_KIND_E2E=1`. With it set, a missing tool (docker, kind, kubectl, helm) or a CNI that does not enforce NetworkPolicy fails the test. Helpers (new): `needTools`, `run(ctx, name, args...)` with a deadline and output capture, `dumpOnFail` (`kubectl describe` and events).
2. Same file, **cluster setup** (new `ensureCluster`): use `ARD_KIND_CLUSTER` if set, else write a kind config to `t.TempDir()` and run `kind create cluster` with a pinned node image digest. Pin kind version, node image digest and registry image digest as constants. These must be fetched by the worker, who has network, from the kind release notes and Docker Hub. Verify whether kindnet enforces `ipBlock.except` for the pinned kind. If it does not, install Calico (`disableDefaultCNI`, pinned manifest digest). Start a local registry container for pushing by digest. `ARD_KIND_KEEP=1` skips teardown. The k8s minimum is 1.30 (values.yaml comment).
3. Same file, **outer mode**: build and push by digest the ard image (root `Dockerfile`, read-only use) and a harness image `FROM` the ard digest plus the test binary (`go test -c -tags integration`, `CGO_ENABLED=0`, node arch). `helm install deploy/charts/ard-serve` with `executor.kind=k8s`, `instance.dryRun=false`, `storage.accessMode=ReadWriteOncePod`, `apiServer` from `kubectl get endpoints kubernetes`, the digests, and `jobs.githubCIDRs`/`jobs.modelAPICIDRs` set to the echo pods' /32s. Scale the Deployment to 0. Per scenario, create a Pod from the Deployment's pod template (`kubectl get deploy -o json`) with the harness image, `-test.run <Scenario>` and `ARD_E2E_INNER=1`, then assert its exit code. Record whether RWOP is enforced (kind's default provisioner is not CSI); if not, mark that scenario informational.
4. Same file, **inner mode** (`ARD_E2E_INNER=1`): build the fixture repo with `gittest`, serve it over smart HTTP (`git http-backend` behind `net/http/cgi`) accepting only tokens minted by `github/fake` and not revoked, and build the kube executor (`kube.New`, `kube.Options.GitBase` pointing at the harness) from the chart's env, following the flow in `internal/servecmd/worker_e2e_test.go`.
5. New `internal/executor/kube/e2e/testdata/stub-agent.sh` (embedded via `go:embed`): the ard image's `sh`, modes `hello`, `sleep`, `fifo`, `big`, `probe`, `marker` as in the comment plan. It prints `env`, `/proc/self/mountinfo` and the token/key searches for the leak assertions.
6. Scenarios, as in the comment plan (design revision 3): HappyPath, AgentHasNoToken, TokenRevokedAfterPre, WrongKey, PostRefusesEarly, MissingResultIsGone, OversizeFails, ControllerRestart, NetworkFalseEgress, Ingress, Admission (`kubectl apply --dry-run=server`), ReadWriteOncePod. Test-only fixtures (echo pods in `default`, a test NetworkPolicy labelled `ard-e2e/git`) are created by the test, never added to the chart. The `marker` assertion is marked pending with a `t.Log` or `t.Skip` citing the design gap (see Risk).
7. `Makefile`: add `kind-e2e` to `.PHONY` and a target `ARD_KIND_E2E=1 go test -tags integration -run TestKind ./internal/executor/kube/e2e/ -timeout 30m`, with a comment saying it needs Docker, kind, kubectl and helm and is not part of `make test`.
8. Check `go vet -tags integration ./internal/executor/kube/e2e/` and `make lint test` pass; the default build must compile with no tag.

### Test plan
- This is the test. Compile check: `make lint test` and `go vet -tags integration ./internal/executor/kube/e2e/`.
- Acceptance (human, needs Docker, ~4 CPU, 8 GiB): `make kind-e2e`. Cases are the scenarios in step 6; each has an exit-code or output assertion as listed in the issue comments.
- Edge and failure cases: `ARD_KIND_E2E` unset means skip; set with a tool missing means fail; NetworkPolicy not enforced means fail; every wait has a deadline and dumps describe and events.
- CI: a human adds the `.github/` workflow; out of scope.

### Risk
Medium. Slow (~10 min), pull-flaky and needs Docker, so it stays out of `make test` and Jenkins. No protected path is touched if the worker follows step 1 to 8 (Makefile and new test directory only). Security-sensitive assertions (key and token leakage) are the point of the test. Design gap: `post` cannot observe the agent's termination and the done marker is agent-writable, so the `marker` assertion is pending until the owner decides. kind storage may not enforce RWOP.

### Size
L (~650 to 900 changed lines): `internal/executor/kube/e2e/kind_integration_test.go`, `internal/executor/kube/e2e/testdata/stub-agent.sh`, `Makefile`.
