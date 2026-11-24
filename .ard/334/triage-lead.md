---
role: lead
status: done
attempt: 1
next: [triage:engineer]
worker: engineer
---
The cve scanner found a high-severity ReDoS advisory in pymdown-extensions. That package is installed for the docs site. It is not listed in docs/site/requirements.txt. It comes in through the pinned mkdocs-material, and mkdocs.yml does not enable any pymdownx extension directly. The fix only touches docs-site dependencies, so the engineer can plan and implement it alone.

## Routing
- triage:engineer: pick the smallest change that installs the fixed release. That means either an explicit pin for pymdown-extensions or a mkdocs-material bump that allows it. Check that the docs site still builds.
- worker engineer: edit docs/site/requirements.txt and verify the docs build.

## Open issues
- The fixed release is a new major version (10.x to 11.x). The pinned mkdocs-material may not accept it, so mkdocs-material may also need a bump. The docs build should be checked by hand because the configured test target only covers Go code.
- No architect role exists in this repository, so nobody will review the major-version bump at design level. It only affects the docs toolchain.
