---
role: engineer
status: done
attempt: 1
---
Adds a lower-bound constraint requiring the fixed idna release to the docs site requirements, next to the existing certifi entry. Size S, risk low because it only affects the documentation build.

## Open issues
- The fixed version (3.15) is taken from the scanner report; the worker should confirm that the resolver accepts it.
