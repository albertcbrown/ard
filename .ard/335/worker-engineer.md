---
role: engineer
status: done
attempt: 1
---
Adds an explicit floor for pymdown-extensions at the release that fixes the snippets path-traversal regression. It is pulled in transitively by mkdocs-material, so a floor is the smallest fix.

## Changes
- docs/site/requirements.txt: a comment line and `pymdown-extensions>=10.21.3`, following the existing certifi convention. mkdocs and mkdocs-material pins are unchanged.

## Testing
Installed the requirements in a fresh venv: no resolver conflict, pymdown-extensions 10.21.3 installed. `mkdocs build --strict` succeeded. No code tests apply.

## Open issues
None.
