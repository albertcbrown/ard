---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The CVE scanner reported a vulnerable pymdown-extensions version in the documentation site's Python requirements. The fix is a version bump within the same major release, so only the engineer needs to plan it and the engineer worker can make the change.

## Routing
- triage:engineer: pymdown-extensions is not pinned in docs/site/requirements.txt and comes in through mkdocs-material. Decide whether to add an explicit minimum pin at the fixed release or to raise mkdocs-material, and say how to check that the docs site still builds.
- worker engineer: narrow dependency change in docs/site/requirements.txt.

## Open issues
- The jump is from 10.9 to 10.21.3, which is several minor releases. The engineer should check that this version works with the pinned mkdocs-material. The vulnerable snippets extension only affects the docs build, not the shipped product.
