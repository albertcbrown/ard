---
role: engineer
status: needs-human
attempt: 1
---
No plan was written. The change is docs-only (size S, risk low), but the text of the design decisions it must record is not in the repository, so a worker could only guess at it.

## Open issues
- The design comment's decisions 1 to 12 and the gaps file are not in the repo or the issue comments. The comments only name decisions 4, 6, 7, 9 and 10 by topic, and the owner's later answers cover TLS, upload, stdout, image, settings and the split of #317. Decisions 1 to 3, 5, 8, 11 and 12, the pod and deadline details (gap 13) and gaps 1, 2, 4, 6 and 7 are missing. A maintainer should paste the full design text into the issue, or commit it under docs/, so the worker can copy it faithfully.
- The issue does not say where the decision record lives. docs/ has only proposal.md, layout.md, TODO.md and runbooks/, and no decision-record directory. Please name the file, or confirm that the proposal's Open questions entry is the record.
- The earlier plan comment's upload step is a native sidecar, which the owner's later answer replaces with a plain second container that waits for the done marker. The table's Runtime cell and the details paragraph must follow the owner's answer.
- The earlier plan's line numbers have drifted. Current positions in docs/proposal.md: the Lease mention is at line 89, retries at 93, the executors table at 291 to 297, Job TTL at 317, the Dockerfile bullet at 430, the config yaml at 435, the egress bullet at 484, the second Lease mention at 544, and Open questions at 556. The worker should match on text, not line number.
- The owner-chosen answers the plan should use are TLS option A (self-signed ECDSA cert, fingerprint pinned in each per-job Secret), a plain upload container, stdout from the tee file at out/stdout as an accepted risk, a public agent image with no secrets, and cluster settings as ard serve flags.
