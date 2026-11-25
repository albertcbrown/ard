---
role: engineer
status: needs-human
attempt: 1
---
The remaining review findings ask for many new cluster-level scenarios (wrong key, controller restart, egress, ingress, close-out commit) that cannot be run or verified here, so no code was changed this round.

## Open issues
- Docker is present, but kind is not installed here, so no scenario can run. Only a compile check is possible, and it would not show whether a cluster scenario works. Writing about 500 lines of unrunnable security assertions risks false confidence.
- The close-out commit assertion needs a design choice: the fixture git server refuses receive-pack today, so it must accept pushes for a valid token and say what commit to expect.
- The pinned image digests, `testdata/stub-agent.sh` and the containerd `config_path` check need network access and a real kind run to confirm.
- A maintainer should either narrow the issue to the scenarios already written (NetworkPolicyEnforced, Admission, HappyPath, ReadWriteOncePod), or have the missing scenarios written on a machine with kind and Docker.
