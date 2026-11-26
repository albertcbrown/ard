---
role: engineer
status: done
attempt: 1
---
Adds in-cluster role image builds to the Kubernetes executor: a content-hash tag taken from the role directory at the pinned commit, a registry check, a budgeted builder pod, and a job pod pinned to the image digest. It also passes the agent base as a build arg on the Docker executor and updates the proposal. Size L, risk medium.

## Open issues
- The builder from #448 is a pod shape enforced by an admission policy, not a batch Job, so the plan builds with a pod.
- Role Dockerfiles already take the agent base through an `AGENT_IMAGE` build arg, so the plan passes the `:latest` ghcr base as that arg instead of editing their `FROM` lines.
- If the builder needs more RBAC than the chart grants, that change is under `deploy/`, which is a protected path and needs a human.
- A private package looks the same as a missing one to the registry check, so the first build of each role is repeated until the package is made public.
