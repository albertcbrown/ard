---
role: engineer
status: done
attempt: 1
---
Adds a new digest loop that posts one comment a day on the status issue with queue depth by label, jobs run, failures and spend for the previous UTC day. It is recorded as an event so a restart never reposts. The plan has six steps with tests. Size M, risk low.

## Open issues
- The digest covers the previous complete UTC day and posts as a new comment each day; a maintainer who wants the pinned issue body edited instead should say so.
- Queue depth counts open issues per `ard:` label from one GitHub list call; docs/proposal.md gets a short update, and the README needs none unless it lists loops.
