---
role: engineer
status: done
attempt: 1
---
Plans a new internal/versioncheck test package that runs the version workflow's plan and tag scripts against bare-repo fixtures and a fake gh. It also adds the Makefile describe filter, a build-info fallback for the version string, and docs updates. Size L, risk low to medium.

## Open issues
- The layout line goes in docs/layout.md, not CLAUDE.md, because CLAUDE.md now points there, so no protected path is touched.
- The tests need bash 4 or newer and jq, and skip when they are missing.
- If the tests find a bug in the workflow, the worker reports it in the PR and a human fixes the workflow file.
- A maintainer may still split the Makefile and version-string change into a first small PR.
