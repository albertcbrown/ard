---
role: engineer
status: done
attempt: 1
---
Plans an opt-in kind end-to-end test for the Kubernetes executor: one integration-tagged test file, an embedded stub agent script and a `kind-e2e` Makefile target. The test creates its own cluster because the `deploy/` script from the dependency never landed. Size L, risk medium.

## Open issues
- `deploy/kind-up.sh` does not exist and `deploy/` is protected, so the plan has the test create the cluster and keeps the worker out of `deploy/`, `Dockerfile` and `.github/`.
- The worker must fetch and pin the kind version, node image digest, registry image digest and, if kindnet does not enforce egress `ipBlock.except`, a Calico manifest digest.
- The `marker` assertion is pending: the post sidecar cannot see the agent terminate and the done marker is agent-writable. The owner should decide.
- A human must add the CI workflow and run `make kind-e2e` to accept the change; `make lint test` only checks compilation.
