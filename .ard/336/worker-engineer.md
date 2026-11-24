---
role: engineer
status: done
attempt: 1
---
Adds a lower-bound constraint on idna to the docs site requirements so the documentation build resolves the release that fixes the reported advisory.

## Changes
- docs/site/requirements.txt: added `idna>=3.15` with a comment, after the certifi entry.

## Testing
Confirmed with `pip index versions idna` that 3.15 exists. I did not run a dry-run resolve or `mkdocs build`. The Go tests are unaffected and were not run.

## Open issues
None.
