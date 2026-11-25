---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This is the second half of the Kubernetes executor. It adds the executor in the kube package: building the pod, watching it, pulling the result back, mapping pod status, following logs, and cleanup and sweep. The issue lists four earlier parts as dependencies (the workspace package, safe tarballs, scoped tokens and the kube API client), and all four are already on main, so the work can start now. The only triage planner in this repository is engineer, so engineer writes the plan and implements it.

## Routing
- triage:engineer: turn the proposed design into concrete steps and tests against the existing workspace, tarball, jobpod and kube API fake packages, and settle the scope cut if the change grows too large.
- worker engineer: implement the executor, its shared type additions and the layout doc entry, with tests that use only the fake API and a local test server.

## Open issues
- The change is large, around 1,300 lines by the issue's own estimate, and it is a state machine spread across the controller, the kubelet and the pod. The plan already says to move the log follower and the sweep into a follow-up if the change passes about 1,500 lines. A maintainer should expect a big pull request.
- No architect or documentation-planner role exists here. The design review the issue refers to was done outside the pipeline, so the engineer planner is the only automated check on it.
- Pod network labelling waits on the separate change that adds a per-role network field. The plan treats that label as conditional.
