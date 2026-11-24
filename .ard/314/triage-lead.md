---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
This adds a new stdlib-only leaf package for streaming tar.gz pack and safe unpack, plus a docs/layout.md entry. The design it depends on is already recorded in docs/proposal.md (part 1 merged), so the work can start now. Only the engineer triage role is needed before the engineer worker implements it.

## Routing
- triage:engineer: write the implementation steps and test plan for the new package (pack, unpack through os.Root, limits, typed errors, fuzz test). Base them on the latest design revision, not the superseded earlier plan in the comments.
- worker engineer: new Go package with table and fuzz tests, and a layout doc update.

## Open issues
- The comments hold two implementation plans. The later one supersedes the first: the swap helper moves to another issue, and pack skips special files instead of failing. The issue body still lists the swap helper, so the plan should state clearly which scope applies.
- This code is the security boundary for agent output (unpacking runs as the controller uid), so the reviewer should look closely at the rejection cases and the fuzz test.
- The package adds shared types that later parts of the Kubernetes executor will use. No architect role exists here, so a maintainer may want to confirm the API shape.
