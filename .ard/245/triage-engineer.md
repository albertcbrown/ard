---
role: engineer
status: needs-human
attempt: 1
---
No plan was written because the work hinges on design decisions that the issue leaves open and that a maintainer should make. Once they are made, the work looks like size L with medium risk.

## Open issues
- Registry: which registry holds role images, and is it public or private? A private one needs imagePullSecrets support in the executor and the chart, and the proposal's statement that no pull secrets are used would change.
- Base image: role Dockerfiles must start from a published agent image on the cluster path, because the local-only image is not pullable there. A maintainer should say which published image and tag or digest they use.
- Build timing: are role images built in CI and pinned by digest, or built at runtime? This decides whether the Jenkinsfile and deploy/ change, which are protected and need human approval.
- Once these are settled, re-triage so the engineer can write concrete steps. Alternatively, split this into a design decision first and then the implementation.
