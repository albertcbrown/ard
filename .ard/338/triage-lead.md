---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A cve scanner finding reports a medium-severity XSS advisory for the mkdocs-material theme pinned in the documentation site's requirements file. The fix is a version bump of that pin to the advisory's fixed release. It touches only the docs build, so engineer planning is enough.

## Routing
- triage:engineer: confirm the fixed version, its compatibility with the pinned mkdocs release, and how to verify the docs site still builds.
- worker engineer: update the pin in the docs site requirements file.

## Open issues
- The bump is a minor-version jump within 9.x, from 9.5 to 9.7, not a patch release. The engineer should check that it still works with the pinned mkdocs 1.6.1 and that the site builds without new warnings.
