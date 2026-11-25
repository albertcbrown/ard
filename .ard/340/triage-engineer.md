---
role: engineer
status: done
attempt: 1
---
Adopts the issue's design as the implementation plan for the Kubernetes executor package: shared type additions, pod builder, status mapper, watcher with result pull, result swap, logs, cleanup and sweep, with fake-API tests. Size L, risk high because it is a credential-handling state machine across the controller, kubelet and pod, though no protected paths are touched.

## Open issues
- The design uses label keys and Secret item names that differ from the existing contract package, which uses the `ard.albertcbrown/` label prefix and a manifest item. The plan follows the contract package.
- The pod network label depends on a per-role network field that does not exist yet. The plan sets it only if the current network value maps cleanly, otherwise leaves it for a follow-up.
- If the diff passes about 1,500 lines, the log follower and sweep should move to a follow-up, as the design says.
