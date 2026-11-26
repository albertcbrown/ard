---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the executor half of the role image work. The Kubernetes executor should build a role's Dockerfile in the cluster, using the builder Job, and run the job on the image it produces instead of ignoring the Dockerfile. The builder dependency (#448) has already merged, so the work can start now. It goes to the engineer for a plan and then to the engineer worker.

## Routing
- triage:engineer: plan the content-hash image tag, the registry check before a build, the build Job with a time budget and clear failure causes, the digest-pinned pod image, the base-image build arg for the Docker executor, fake-kube-client tests and the proposal update.
- worker engineer: Go change in the kube executor (and the shared role-source and Dockerfile handling it needs), with unit tests and the docs/proposal.md update.

## Open issues
- The only triage roles in this repository are lead and engineer, so no architect role is available. This change spans the executors, the role source and the registry interaction, so the engineer's plan has to cover those design choices.
- If the work has to change deploy/ (for example, builder RBAC in the chart), the PR will wait for a human approval under merge.protected_paths.
- The issue lists a known residual risk to record in the proposal: a `:latest` base tag can hide a change to the base image under an unchanged role hash.
