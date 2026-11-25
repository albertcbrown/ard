---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds an opt-in kind end-to-end test for the Kubernetes executor, with a Makefile target, under the owner-approved design. Its dependencies (serve wiring, the chart's jobs namespace and policies, and the agent and pod images) are now on main, so it can go to the engineer for planning and implementation.

## Routing
- triage:engineer: turn the design revision 3 plan from the comments into implementation steps against the current kube executor, chart and images, and pin the kind, node image, registry and CNI versions.
- worker engineer: the integration-tagged test file, stub agent script, fixtures and the `kind-e2e` Makefile target.

## Open issues
- The issue expects a cluster-creation script under `deploy/` from #319 or a human PR. #319 merged without one, and `deploy/` is a protected path. The plan should either have the test create the cluster itself or wait for a human to add the script. If the worker touches `deploy/`, the PR needs human approval.
- The CI workflow under `.github/` is out of scope for the worker. A human adds it.
- The comments record an open design gap: the post sidecar cannot see the agent container terminate, and the done marker is writable by the agent. The plan marks that one assertion as pending until the owner decides. A maintainer should confirm that this is acceptable.
- The test needs Docker and a cluster, and it does not run in `make test` or Jenkins. The worker's `make lint test` run will only check that it compiles under the default tags, so a human has to run `make kind-e2e` to accept the change.
