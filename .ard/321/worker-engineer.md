---
role: engineer
status: needs-human
attempt: 1
---
The two blocking review findings (missing wrong-key, controller-restart and network scenarios; no close-out commit assertion) ask for a large amount of new cluster-level test code that cannot be run or verified in this environment, so no code was changed this round.

## Open issues
- Everything the review asks for needs Docker, kind, kubectl and helm to check. Only a compile check is possible here, which would not show whether a scenario works.
- The close-out commit assertion needs a design decision: the fixture git server now refuses receive-pack, so it must accept pushes for a valid token and say what commit content to expect.
- A maintainer should either narrow the issue to the scenarios already written (network policy, admission, happy path, ReadWriteOncePod), or have the added scenarios written and run on a machine with a kind cluster.
- The unpinned images, the stub-agent.sh file and the containerd config_path check also need a human run to confirm.
