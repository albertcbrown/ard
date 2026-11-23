---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The starter eng-review reviewer template that ard ships should run the repository's own lint entry point, and use a tool of its own choosing only when the repository has none. The change is limited to one embedded prompt file and its tests in internal/roles, so the engineer can plan it without other triage roles.

## Routing
- triage:engineer: plan the prompt wording, how the reviewer finds the lint entry point (Makefile, README, CI), the fallback note under Checks, and the updates to the eng-review role tests.
- worker engineer: edit the starter prompt template and its tests.

## Open issues
- The target is the starter template under internal/roles/reviewers/, not this repository's own .ard/reviewers/eng-review/prompt.txt, which is protected and is not part of this change. A maintainer may want to make the same change to the live copy by hand.
