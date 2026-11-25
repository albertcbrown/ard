---
role: engineer
status: done
attempt: 1
---
Plans the serve wiring for the Kubernetes executor from design revision 3, with no listener. It covers executor selection, cluster flags, refusing a personal access token, config drift, role source in the job builders, new close-out causes, the orphan sweep, token redaction and docs. Size L at the lower end, risk medium.

## Open issues
- The earlier plan was corrected against the landed code. The kube executor has no `Prune` method and the Reaper's executor interface requires one, so the plan adds it. The type is `kube.Executor`, and the kube options also need the repo and instance values.
- The reviewer loop also reads the process environment, so it needs the redaction change too.
- The issue says S-M, but the plan is L. A maintainer may want to split it, for example the causes and builders first and the serve flags second.
