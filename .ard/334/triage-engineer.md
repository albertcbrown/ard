---
role: engineer
status: done
attempt: 1
---
Raises the docs-site dependency floor so the fixed pymdown-extensions release is installed, bumping mkdocs-material if it blocks the new major version. The plan has three steps and a strict docs build as the check. Size S, risk low.

## Open issues
- mkdocs-material 9.5.50 probably caps pymdown-extensions below 11, so the worker must find a compatible material release with a pip dry run, and that needs network access.
- If no material release allows the fixed version, the work is blocked upstream.
