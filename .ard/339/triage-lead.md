---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
A CVE scanner finding against pymdown-extensions, a transitive dependency of the documentation site build (pulled in through mkdocs-material). The fix is a version floor in the docs site requirements, in the same style as the existing certifi pin, so only the engineer needs to plan it.

## Routing
- triage:engineer: confirm the fixed release (10.16.1 or later, same major version) works with the pinned mkdocs-material, and plan a docs site build check.
- worker engineer: add the version floor to docs/site/requirements.txt.

## Open issues
- The upgrade goes from 10.9 to 10.16, a minor-version jump within the same major version. It only affects the docs site toolchain, not the shipped code.
