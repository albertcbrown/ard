---
role: engineer
status: done
attempt: 1
---
Bumps the pinned mkdocs-material theme in the docs site requirements file to the advisory's fixed release, then verifies with the strict docs build and drift test. Size S, risk low.

## Open issues
- The jump is from 9.5 to 9.7, a minor release. I could not install packages here, so compatibility with the pinned mkdocs 1.6.1 is unverified and the worker must confirm it with the strict build.
