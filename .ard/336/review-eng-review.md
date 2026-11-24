---
role: eng-review
status: done
attempt: 1
verdict: approve
---
The change adds `idna>=3.15` with an explanatory comment to the docs requirements, exactly as plan.md describes, and it matches the existing certifi entry. Approved.

## Checks
- Lint: no lint applies; the change touches only a requirements text file.
- Resolve check: `pip install --dry-run -r docs/site/requirements.txt` in a throwaway venv resolved with no conflict and selected idna 3.20, which is at or above the fixed 3.15.
- Diff coverage: skipped; the change adds no code lines.
- I did not run `mkdocs build`.
