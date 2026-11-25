---
role: engineer
status: done
attempt: 1
---
Adds a direct constraint on the fixed pymdown-extensions release to the docs requirements and raises the mkdocs-material pin, because 9.5.50 and 9.6.x cannot coexist with pymdown-extensions 11.

## Changes
- docs/site/requirements.txt: mkdocs-material 9.5.50 -> 9.7.0 (lowest release whose requirements allow pymdown-extensions 11); new `pymdown-extensions>=11.0.0` with an advisory comment. mkdocs and certifi lines are unchanged.

## Testing
- Dry-run resolve in a fresh venv outside the repo: 9.5.50 and 9.6.23 with the new constraint fail (ResolutionImpossible); 9.7.0 resolves, picking pymdown-extensions 13.1.2.
- Installed the new requirements and ran `mkdocs build --strict` against mkdocs.yml: succeeds with no warnings.
- `make test` was not run; the change touches only docs dependencies.

## Open issues
- Another open issue also bumps the mkdocs-material pin; the two changes may conflict on the same line.
