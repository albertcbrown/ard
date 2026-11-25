---
role: engineer
status: done
attempt: 1
---
Re-plans the kind end-to-end test for the narrowed scope: network policy enforcement, admission, the happy path and ReadWriteOncePod. It adds the missing stub agent script, pins every fixture image by digest and settles the containerd registry config check. Size M, risk medium.

## Open issues
- The worker cannot run the cluster scenarios, so a human must run `make kind-e2e` for acceptance.
- Digests and the kind version must be resolved from the network by the worker and refreshed by a human over time.
- The scenarios moved to #437 are excluded, including the close-out commit assertion and the pending marker case.
- A human adds the CI workflow under `.github/`.
