---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the last slice of the default agent image work. It lets roles that ship their own Dockerfile run under the Kubernetes executor by building those images and making them pullable by the cluster. Both dependencies appear to have landed on main: the local ard-agent image (part 55a) and the Kubernetes executor series, including its agent and pod images and image pins. So the work can start, and the engineer plans it.

## Routing
- triage:engineer: choose how role images build on the published agent image instead of the local-only one, where they are built and pushed, how the k8s executor picks the image for each role, and the tests and proposal update.
- worker engineer: executor, build and docs changes, with tests.

## Open issues
- The repository has no architect triage role, so the engineer's plan has to make the design choices itself. These are: the base image for role images on the cluster path, the registry and whether it is public, and whether role images are built in CI and pinned by digest or built at runtime. A maintainer should check these choices in the plan before the worker starts.
- The change will probably touch Jenkinsfile, Dockerfile or deploy/. Those are protected paths, so the pull request will wait for a human approval.
- The proposal currently says that no imagePullSecrets are used because both images are public. If role images go to a private registry, that statement and the chart both need to change.
