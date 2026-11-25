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
      The wrong-key and controller-restart scenarios that issue #321 asks for are still not written; the worker pushed no code change this round.
      plan.md step 6 also names TokenRevokedAfterPre, PostRefusesEarly, MissingResultIsGone, OversizeFails, NetworkFalseEgress and Ingress. TestKind runs only NetworkPolicyEnforced, Admission, HappyPath and ReadWriteOncePod. Add WrongKey and ControllerRestart (a second harness run adopts and reaps the sleeping job) and the egress and ingress scenarios, or have the owner narrow the issue.
  - file: "internal/executor/kube/e2e/inner_integration_test.go"
    line: 100
    severity: high
    blocking: true
    message: |
      HappyPath still does not check that close-out commits to the remote, which the issue requires.
      It asserts only the pulled result. The fixture git server refuses receive-pack, so a push cannot be observed. Allow pushes for a valid token, or check the commit another way, and assert the commit content.
  - file: "internal/executor/kube/e2e/inner_integration_test.go"
    line: 100
    severity: medium
    blocking: false
    message: |
      plan.md step 5's testdata/stub-agent.sh (modes hello, sleep, fifo, big, probe, marker) still does not exist; the stub is inline. Add it together with the missing scenarios.
  - file: "internal/executor/kube/e2e/kind_integration_test.go"
    line: 142
    severity: medium
    blocking: false
    message: |
      Images and versions are still unpinned (registry:2, kind's default node image), against plan.md step 2, and kindnet's enforcement of ipBlock.except is not tested. Pin the digests and test it.
  - file: "internal/executor/kube/e2e/kind_integration_test.go"
    line: 154
    severity: low
    blocking: false
    message: |
      containerd's certs.d is written without checking that config_path is enabled in the node's containerd, so pulls from localhost:5001 may fail on first run. Needs a human run to confirm.
---
Still changes: the head commit adds no code over the previous round (the worker's report says it changed nothing and is waiting for a human), so both blocking findings remain.

## Checks
- Lint and diff coverage were not re-run: the code is identical to the previous round, and the tests need Docker, kind and a cluster. Last round `go vet -tags integration` and `gofmt -l` on the package were clean.

## Previous findings
- internal/executor/kube/e2e/kind_integration_test.go (missing wrong-key, restart, network scenarios): still present
- internal/executor/kube/e2e/inner_integration_test.go (no close-out commit assertion): still present
- internal/executor/kube/e2e/inner_integration_test.go (stub-agent.sh missing): still present
- internal/executor/kube/e2e/kind_integration_test.go (unpinned images): still present
- internal/executor/kube/e2e/kind_integration_test.go (containerd config_path): still present
