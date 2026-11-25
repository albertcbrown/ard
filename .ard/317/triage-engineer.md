---
role: engineer
status: done
attempt: 1
---
Plans only the Kubernetes API layer: minimal types, a client interface, an in-cluster REST implementation and an in-memory fake, with tests against an httptest TLS server. Size L (about 750 lines), risk low to medium.

## Open issues
- The issue body describes the whole executor, but the plan covers only the API layer, as the latest design revision narrows it. A maintainer should update the body or open the executor part separately.
- The older plan's job API, key table, input tarball and TLS parts were ignored because design revision 3 replaces them.
- Native sidecar versus a plain upload container, and how the model credential reaches the pod, are executor decisions that a maintainer must settle before that part.
