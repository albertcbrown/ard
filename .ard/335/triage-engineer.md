---
role: engineer
status: done
attempt: 1
---
Adds an explicit minimum pin for pymdown-extensions at the fixed release in the docs requirements file, next to the existing certifi pin. Size S, risk low because it only affects the docs build.

## Open issues
- The worker should run a strict docs build after the bump, since several minor releases are skipped. I did not run one while planning.
