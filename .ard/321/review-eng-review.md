---
role: eng-review
status: done
attempt: 1
verdict: changes
findings:
  - file: "internal/executor/kube/e2e/kind_integration_test.go"
    line: 105
    severity: high
    blocking: true
    message: |
      The issue's wrong-key and controller-restart scenarios are not written (the worker's report admits it).
      Issue #321 asks for: a pod with a wrong key is refused, and a controller restart kills the orphan. plan.md step 6 also names WrongKey, TokenRevokedAfterPre, PostRefusesEarly, MissingResultIsGone, OversizeFails, ControllerRestart, NetworkFalseEgress and Ingress. TestKind runs only NetworkPolicyEnforced, Admission, HappyPath and ReadWriteOncePod. Add at least WrongKey and ControllerRestart (second harness run adopting and reaping the sleeping job), and the egress/ingress scenarios, or get the owner to narrow the issue.
  - file: "internal/executor/kube/e2e/inner_integration_test.go"
    line: 100
    severity: high
    blocking: true
    message: |
      HappyPath does not check that close-out commits to the remote, which the issue requires ("a job fetches, runs, uploads, and close-out commits").
      It asserts only the pulled result (hello.txt and role file). The fixture git server also refuses receive-pack, so a commit/push can never be observed. Allow pushes for a valid token, or check the commit another way, and assert the commit content.
  - file: "internal/executor/kube/e2e/inner_integration_test.go"
    line: 100
    severity: medium
    blocking: false
    message: |
      plan.md step 5 creates testdata/stub-agent.sh (embedded, with modes hello, sleep, fifo, big, probe, marker). It does not exist; the stub is presumably inline. The missing modes are what the absent scenarios need. Add the file along with those scenarios.
  - file: "internal/executor/kube/e2e/kind_integration_test.go"
    line: 142
    severity: medium
    blocking: false
    message: |
      Images and versions are unpinned (registry:2 tag, kind's default node image, kindnet enforcement of ipBlock.except not verified), against plan.md step 2. A flaky or silently weaker run is likely. Pin the registry image digest and the node image, and test ipBlock.except, since the chart's egress rules depend on it.
  - file: "internal/executor/kube/e2e/kind_integration_test.go"
    line: 154
    severity: low
    blocking: false
    message: |
      The registry container is attached to the kind network after the cluster is created and containerd's certs.d is written without checking that config_path is enabled in the node's containerd. If not, pulls from localhost:5001 fail on first run. The worker flagged this; it needs a human run to confirm.
---
Changes: the change is a compiling, opt-in scaffold, but it covers only a small part of the issue. The wrong-key and controller-restart scenarios and the close-out commit assertion are missing, and the cluster test was never run.

## Checks
- `go vet -tags integration ./internal/executor/kube/e2e/`: clean. `gofmt -l` on the package: clean.
- `make lint` was not re-run; the worker reports that a pre-existing gofmt parse error in internal/roles/testdata appears there.
- Diff coverage: skipped. The tests need Docker, kind and a cluster and cannot run here. The entry points skip without ARD_KIND_E2E=1.
- The Makefile edit (the `kind-e2e` target, with `.PHONY` updated) matches plan.md step 7.
