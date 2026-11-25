---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds a new internal Kubernetes REST layer: minimal API types, a client interface, an in-cluster transport and an in-memory fake, all under internal/executor/kube. It is a self-contained internal package that changes no public interface or stored data, and the packages it builds on (parts 2 to 4: workspace, tarball, scoped token) are already on main, so the engineer can plan it now.

## Routing
- triage:engineer: settle the exact scope for this part and write the steps and the test plan against the fake and an httptest TLS server.
- worker engineer: new Go package with unit tests and a docs/layout.md entry.

## Open issues
- The scope is unclear. The issue body describes the whole executor (pod builder, status mapping, log follower, cleanup), but the latest comment narrows part 6A to the API layer alone and leaves the executor for a later part. The engineer should plan only the API layer and say so. A maintainer should update the body if that is not what they meant.
- The latest comment says design revision 3 replaces the earlier plan wherever that plan covers the job API, the key table, the input tarball or TLS. The engineer should ignore those parts of the older plan.
- The open design questions (native sidecar or plain upload container, how the credential reaches the pod) belong to the executor part, not this one. This repository has no architect triage role, so a maintainer must answer them before the executor part is routed.
