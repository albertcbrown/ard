---
role: engineer
status: done
attempt: 1
---
Bumps the docs site's mkdocs-material pin to the advisory's fixed release, which closes the search-suggestions XSS finding.

## Changes
- docs/site/requirements.txt: mkdocs-material 9.5.50 -> 9.7.7. The mkdocs 1.6.1 and certifi pins are unchanged, since pip resolved them without conflict.

## Testing
- In a throwaway virtualenv outside the repo, ran pip install -r docs/site/requirements.txt. It installed mkdocs 1.6.1 and mkdocs-material 9.7.7.
- mkdocs build --strict succeeded with exit 0 and no config errors. The build prints the theme's notice about MkDocs 2.0, but strict mode does not treat it as a warning.
- go test ./internal/docscheck/... passes.
- No other references to the old version remain.

## Open issues
None.
